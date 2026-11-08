package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"syncthing-server/internal/config"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestAdminConnectivity(t *testing.T) {
	var mu sync.Mutex
	settings := map[string]any{"globalAnnounceEnabled": true, "relaysEnabled": true, "natEnabled": true, "localAnnounceEnabled": true}
	var patches []map[string]bool
	readFailure, writeFailure := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-API-Key") != "private-key" {
			t.Error("missing daemon API key")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case r.Method == "GET" && r.URL.Path == "/rest/config/options":
			if readFailure {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(settings)
		case r.Method == "PATCH" && r.URL.Path == "/rest/config/options":
			if writeFailure {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			var patch map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(patch) == 0 {
				t.Error("empty options patch")
			}
			for key, enabled := range patch {
				if key != "globalAnnounceEnabled" && key != "relaysEnabled" && key != "natEnabled" {
					t.Errorf("unexpected field: %s", key)
				}
				settings[key] = enabled
			}
			patches = append(patches, patch)
		default:
			t.Errorf("unexpected daemon request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := syncthing.NewClient(server.URL, "private-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	admin, err := db.UpsertUser(context.Background(), "https://issuer.example", "admin", "Admin", "a@example", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.UpsertUser(context.Background(), "https://issuer.example", "other", "Other", "o@example", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(context.Background(), "admin-token", admin.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(context.Background(), "other-token", other.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	files, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	h := web.New(config.Config{BaseURL: "https://example.test", Admins: []config.Admin{{Issuer: admin.Issuer, Subject: admin.Subject}}}, db, client, nil, files)
	adminCookie := &http.Cookie{Name: "session", Value: "admin-token"}
	otherCookie := &http.Cookie{Name: "session", Value: "other-token"}
	token := csrf(t, h, adminCookie)

	checkState := func(global, relays, nat bool) {
		t.Helper()
		res := serve(h, adminCookie, "GET", "/admin?tab=settings", nil)
		body := res.Body.String()
		if res.Code != 200 || !strings.Contains(body, `<h2 id="connectivity-heading">Connections</h2>`) || !strings.Contains(body, `action="/admin/connectivity"`) || strings.Contains(body, "Disable all three") {
			t.Fatalf("admin settings: %d %s", res.Code, body)
		}
		for _, field := range []struct {
			name, original, label string
			value                 bool
		}{
			{"globalDiscovery", "originalGlobalDiscovery", "Global Discovery", global},
			{"relays", "originalRelays", "Enable Relaying", relays},
			{"natTraversal", "originalNATTraversal", "Enable NAT traversal", nat},
		} {
			original := "false"
			if field.value {
				original = "true"
			}
			if !strings.Contains(body, `name="`+field.original+`" value="`+original+`"`) {
				t.Errorf("incorrect original %s value in %s", field.name, body)
			}
			fragment := `name="` + field.name + `" value="on" `
			if field.value {
				fragment += "checked"
			}
			fragment += ">" + field.label
			if !strings.Contains(body, fragment) {
				t.Errorf("incorrect %s checkbox in %s", field.name, body)
			}
		}
		if !strings.Contains(body, `<button type="submit">Save</button>`) {
			t.Fatal("missing shared Save button")
		}
	}
	form := func(global, relays, nat bool) url.Values {
		values := url.Values{
			"csrf":                    {token},
			"originalGlobalDiscovery": {"false"},
			"originalRelays":          {"false"},
			"originalNATTraversal":    {"false"},
		}
		for _, field := range []struct {
			name, original string
			enabled        bool
		}{
			{"globalDiscovery", "originalGlobalDiscovery", global},
			{"relays", "originalRelays", relays},
			{"natTraversal", "originalNATTraversal", nat},
		} {
			if field.enabled {
				values.Set(field.original, "true")
				values.Set(field.name, "on")
			}
		}
		return values
	}
	post := func(values url.Values, want int) {
		t.Helper()
		res := serve(h, adminCookie, "POST", "/admin/connectivity", values)
		if res.Code != want {
			t.Fatalf("POST %v: got %d want %d: %s", values, res.Code, want, res.Body.String())
		}
		if want == http.StatusSeeOther && res.Header().Get("Location") != "/admin?tab=settings" {
			t.Fatalf("unexpected redirect: %s", res.Header().Get("Location"))
		}
	}
	checkPatch := func(index int, key string, enabled bool) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(patches) != index+1 || len(patches[index]) != 1 {
			t.Fatalf("unexpected patches: %v", patches)
		}
		if value, ok := patches[index][key]; !ok || value != enabled {
			t.Fatalf("patch %d: %v, want %s=%t", index, patches[index], key, enabled)
		}
	}

	checkState(true, true, true)
	for _, path := range []string{"/admin", "/admin?tab=peers"} {
		res := serve(h, adminCookie, "GET", path, nil)
		if res.Code != 200 || strings.Contains(res.Body.String(), `action="/admin/connectivity"`) {
			t.Fatalf("connectivity controls leaked into %s: %d", path, res.Code)
		}
	}
	if res := serve(h, adminCookie, "GET", "/", nil); res.Code != 200 || strings.Contains(res.Body.String(), `action="/admin/connectivity"`) || !strings.Contains(res.Body.String(), `<code data-device-id>SERVER</code><button class="text-button copy-id" type="button" data-copy-id aria-label="Copy server device ID"><svg`) || strings.Contains(res.Body.String(), `?tab=settings`) {
		t.Fatalf("dashboard exposed admin setting or omitted server copy control: %d %s", res.Code, res.Body.String())
	}
	mu.Lock()
	settings["relaysEnabled"] = false
	mu.Unlock()
	checkState(true, false, true)
	mu.Lock()
	settings["relaysEnabled"] = true
	readFailure = true
	mu.Unlock()
	unavailable := serve(h, adminCookie, "GET", "/admin?tab=settings", nil)
	if unavailable.Code != 200 || !strings.Contains(unavailable.Body.String(), "Syncthing's settings could not be read") || strings.Contains(unavailable.Body.String(), `action="/admin/connectivity"`) {
		t.Fatalf("unavailable settings exposed form: %d %s", unavailable.Code, unavailable.Body.String())
	}
	mu.Lock()
	readFailure = false
	mu.Unlock()

	otherForm := form(true, true, true)
	otherForm.Set("csrf", csrf(t, h, otherCookie))
	for _, tc := range []struct {
		cookie *http.Cookie
		form   url.Values
		want   int
	}{
		{nil, form(true, true, true), http.StatusUnauthorized},
		{otherCookie, otherForm, http.StatusForbidden},
		{adminCookie, url.Values{"originalGlobalDiscovery": {"true"}}, http.StatusForbidden},
		{adminCookie, url.Values{"csrf": {token}, "action": {"disable"}}, http.StatusBadRequest},
		{adminCookie, url.Values{"csrf": {token}}, http.StatusBadRequest},
	} {
		res := serve(h, tc.cookie, "POST", "/admin/connectivity", tc.form)
		if res.Code != tc.want {
			t.Fatalf("unauthorized/invalid post: got %d want %d", res.Code, tc.want)
		}
	}
	for _, mutate := range []func(url.Values){
		func(v url.Values) { v.Add("originalRelays", "false") },
		func(v url.Values) { v.Set("originalRelays", "maybe") },
		func(v url.Values) { v.Del("originalNATTraversal") },
		func(v url.Values) { v.Add("globalDiscovery", "on") },
		func(v url.Values) { v.Set("relays", "invalid") },
		func(v url.Values) { v.Set("unknown", "true") },
	} {
		values := form(true, true, true)
		mutate(values)
		post(values, http.StatusBadRequest)
	}
	foreign := httptest.NewRequest("POST", "/admin/connectivity", strings.NewReader(form(true, true, true).Encode()))
	foreign.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	foreign.Header.Set("Origin", "https://other.test")
	foreign.AddCookie(adminCookie)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, foreign)
	if res.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.Code)
	}
	if res := serve(h, otherCookie, "GET", "/admin?tab=settings", nil); res.Code != http.StatusForbidden {
		t.Fatalf("non-admin read: %d", res.Code)
	}
	if res := serve(h, nil, "GET", "/admin?tab=settings", nil); res.Code != http.StatusSeeOther {
		t.Fatalf("anonymous read: %d", res.Code)
	}
	mu.Lock()
	if len(patches) != 0 {
		t.Fatalf("rejected requests patched daemon: %v", patches)
	}
	mu.Unlock()

	values := form(true, true, true)
	values.Del("globalDiscovery")
	post(values, http.StatusSeeOther)
	checkPatch(0, "globalAnnounceEnabled", false)
	checkState(false, true, true)

	values = form(false, true, true)
	values.Del("relays")
	post(values, http.StatusSeeOther)
	checkPatch(1, "relaysEnabled", false)
	checkState(false, false, true)

	values = form(false, false, true)
	values.Del("natTraversal")
	post(values, http.StatusSeeOther)
	checkPatch(2, "natEnabled", false)
	checkState(false, false, false)

	post(form(false, false, false), http.StatusSeeOther)
	mu.Lock()
	if len(patches) != 3 {
		t.Fatalf("unchanged form patched daemon: %v", patches)
	}
	settings["globalAnnounceEnabled"] = true
	mu.Unlock()
	values = form(false, false, false)
	values.Set("relays", "on")
	post(values, http.StatusSeeOther)
	checkPatch(3, "relaysEnabled", true)
	checkState(true, true, false)

	values = form(true, true, false)
	values.Del("globalDiscovery")
	values.Del("relays")
	values.Set("natTraversal", "on")
	post(values, http.StatusSeeOther)
	mu.Lock()
	if len(patches) != 5 || len(patches[4]) != 3 || patches[4]["globalAnnounceEnabled"] || patches[4]["relaysEnabled"] || !patches[4]["natEnabled"] {
		t.Fatalf("multiple checkbox changes not saved together: %v", patches)
	}
	mu.Unlock()
	checkState(false, false, true)

	mu.Lock()
	writeFailure = true
	mu.Unlock()
	values = form(false, false, true)
	values.Set("globalDiscovery", "on")
	post(values, http.StatusServiceUnavailable)
	checkState(false, false, true)
	mu.Lock()
	defer mu.Unlock()
	if len(patches) != 5 || settings["localAnnounceEnabled"] != true {
		t.Fatalf("unexpected daemon state after writes: %v (%v patches)", settings, patches)
	}
}
