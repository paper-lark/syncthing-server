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
	patches := 0
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
			if len(patch) != 3 {
				t.Errorf("unexpected patch: %v", patch)
			}
			for key, enabled := range patch {
				if key != "globalAnnounceEnabled" && key != "relaysEnabled" && key != "natEnabled" {
					t.Errorf("unexpected field: %s", key)
				}
				settings[key] = enabled
			}
			patches++
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

	checkState := func(want string, hasActions bool) {
		t.Helper()
		res := serve(h, adminCookie, "GET", "/admin", nil)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "<strong>"+want+"</strong>") {
			t.Fatalf("admin state %s: %d %s", want, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), `action="/admin/connectivity"`) != hasActions {
			t.Fatalf("unexpected admin action availability for %s", want)
		}
	}
	checkState("Enabled", true)
	if res := serve(h, adminCookie, "GET", "/", nil); res.Code != 200 || strings.Contains(res.Body.String(), "Discovery and connectivity") {
		t.Fatalf("dashboard exposed admin setting: %d", res.Code)
	}
	mu.Lock()
	settings["relaysEnabled"] = false
	mu.Unlock()
	checkState("Mixed", true)
	mu.Lock()
	settings["relaysEnabled"] = true
	readFailure = true
	mu.Unlock()
	checkState("Unavailable", false)
	mu.Lock()
	readFailure = false
	mu.Unlock()

	for _, tc := range []struct {
		cookie *http.Cookie
		form   url.Values
		want   int
	}{
		{nil, url.Values{"csrf": {token}, "action": {"disable"}}, http.StatusUnauthorized},
		{otherCookie, url.Values{"csrf": {csrf(t, h, otherCookie)}, "action": {"disable"}}, http.StatusForbidden},
		{adminCookie, url.Values{"action": {"disable"}}, http.StatusForbidden},
		{adminCookie, url.Values{"csrf": {token}, "action": {"invalid"}}, http.StatusBadRequest},
		{adminCookie, url.Values{"csrf": {token}, "action": {"enable", "disable"}}, http.StatusBadRequest},
	} {
		res := serve(h, tc.cookie, "POST", "/admin/connectivity", tc.form)
		if res.Code != tc.want {
			t.Fatalf("unauthorized/invalid post: got %d want %d", res.Code, tc.want)
		}
	}
	foreign := httptest.NewRequest("POST", "/admin/connectivity", strings.NewReader(url.Values{"csrf": {token}, "action": {"disable"}}.Encode()))
	foreign.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	foreign.Header.Set("Origin", "https://other.test")
	foreign.AddCookie(adminCookie)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, foreign)
	if res.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", res.Code)
	}
	if res := serve(h, otherCookie, "GET", "/admin", nil); res.Code != http.StatusForbidden {
		t.Fatalf("non-admin read: %d", res.Code)
	}
	if res := serve(h, nil, "GET", "/admin", nil); res.Code != http.StatusSeeOther {
		t.Fatalf("anonymous read: %d", res.Code)
	}
	mu.Lock()
	if patches != 0 {
		t.Fatalf("rejected requests patched daemon %d times", patches)
	}
	mu.Unlock()

	post := func(action string, want int) {
		t.Helper()
		res := serve(h, adminCookie, "POST", "/admin/connectivity", url.Values{"csrf": {token}, "action": {action}})
		if res.Code != want {
			t.Fatalf("%s: got %d want %d: %s", action, res.Code, want, res.Body.String())
		}
		if want == http.StatusSeeOther && res.Header().Get("Location") != "/admin" {
			t.Fatalf("unexpected redirect: %s", res.Header().Get("Location"))
		}
	}
	post("disable", http.StatusSeeOther)
	checkState("Disabled", true)
	post("enable", http.StatusSeeOther)
	checkState("Enabled", true)
	mu.Lock()
	writeFailure = true
	mu.Unlock()
	post("disable", http.StatusServiceUnavailable)
	checkState("Enabled", true)
	mu.Lock()
	defer mu.Unlock()
	if patches != 2 || settings["localAnnounceEnabled"] != true {
		t.Fatalf("unexpected daemon state after writes: %v (%d patches)", settings, patches)
	}
}
