package web_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"syncthing-server/internal/config"
	"syncthing-server/internal/oidc"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func fixture(t *testing.T) (http.Handler, *store.Store, *http.Cookie) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	u, e := db.UpsertUser(context.Background(), "https://issuer.example", "alice", "Alice", "a@example", true)
	if e != nil {
		t.Fatal(e)
	}
	token := "secret-token-not-an-identity"
	if e = db.CreateSession(context.Background(), token, u.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	files, e := storage.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { files.Close() })
	return web.New(config.Config{BaseURL: "https://example.test", Admins: []config.Admin{{Issuer: u.Issuer, Subject: u.Subject}}}, db, nil, nil, files), db, &http.Cookie{Name: "session", Value: token}
}
func serve(h http.Handler, c *http.Cookie, method, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}
func csrf(t *testing.T, h http.Handler, c *http.Cookie) string {
	t.Helper()
	r := serve(h, c, "GET", "/", nil)
	if r.Code != 200 {
		t.Fatalf("dashboard: %d: %s", r.Code, r.Body.String())
	}
	m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(r.Body.String())
	if len(m) != 2 {
		t.Fatal("missing CSRF form field")
	}
	return m[1]
}
func TestAnonymousCannotReadDashboardOrWrite(t *testing.T) {
	h, _, _ := fixture(t)
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/", 303}, {"GET", "/admin", 303}, {"POST", "/folders", 401}} {
		r := serve(h, nil, tc.method, tc.path, nil)
		if r.Code != tc.want {
			t.Errorf("%s %s: got %d want %d", tc.method, tc.path, r.Code, tc.want)
		}
	}
}
func TestAuthenticatedWritesRequireCSRFAndLogoutInvalidatesSession(t *testing.T) {
	h, db, c := fixture(t)
	u, e := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path string
		form url.Values
	}{{"/folders", url.Values{"name": {"secret"}}}, {"/devices", url.Values{"id": {"AAAAAAA-BBBBBBB-CCCCCCC-DDDDDDD-EEEEEEE-FFFFFFF-GGGGGGG-HHHHHHH"}, "name": {"peer"}}}, {"/logout", nil}} {
		r := serve(h, c, "POST", tc.path, tc.form)
		if r.Code != 403 {
			t.Errorf("%s without CSRF: %d", tc.path, r.Code)
		}
	}
	fs, e := db.UserFolders(context.Background(), u.ID)
	if e != nil || len(fs) != 0 {
		t.Fatalf("unexpected folders: %v %v", fs, e)
	}
	token := csrf(t, h, c)
	r := serve(h, c, "POST", "/logout", url.Values{"csrf": {token}})
	if r.Code != 303 {
		t.Fatalf("logout: %d %s", r.Code, r.Body.String())
	}
	if r = serve(h, c, "GET", "/", nil); r.Code != 303 {
		t.Fatalf("logged-out session read: %d", r.Code)
	}
}
func TestDashboardAndAdminOmitPathsAndSecrets(t *testing.T) {
	h, db, c := fixture(t)
	alice, e := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if e != nil {
		t.Fatal(e)
	}
	bob, e := db.UpsertUser(context.Background(), "https://issuer.example", "bob", "Bob", "b@example", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.CreateFolder(context.Background(), bob.ID, "bob-folder", "Bob files"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateFolder(context.Background(), alice.ID, "alice-folder", "Alice files"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateDevice(context.Background(), alice.ID, "alice-peer", "Alice laptop"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateDevice(context.Background(), bob.ID, "bob-peer", "Bob laptop"); e != nil {
		t.Fatal(e)
	}
	r := serve(h, c, "GET", "/", nil)
	if r.Code != 200 || strings.Contains(r.Body.String(), "Bob files") || !strings.Contains(r.Body.String(), "Alice files") || strings.Contains(r.Body.String(), alice.StorageArea) || strings.Contains(r.Body.String(), bob.StorageArea) {
		t.Fatalf("dashboard leaked ownership or path: %d %s", r.Code, r.Body.String())
	}
	r = serve(h, c, "GET", "/admin", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Bob files") || strings.Contains(r.Body.String(), bob.StorageArea) || strings.Contains(r.Body.String(), "/folders/bob-folder/delete") {
		t.Fatalf("admin overview leaked path or write actions: %d %s", r.Code, r.Body.String())
	}
	r = serve(h, c, "GET", "/?tab=peers", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Alice laptop") || strings.Contains(r.Body.String(), "Bob laptop") || strings.Contains(r.Body.String(), "Alice files") || strings.Contains(r.Body.String(), alice.StorageArea) {
		t.Fatalf("peer dashboard leaked ownership or wrong category: %d %s", r.Code, r.Body.String())
	}
	r = serve(h, c, "GET", "/admin?tab=peers", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Bob laptop") || strings.Contains(r.Body.String(), "Bob files") || strings.Contains(r.Body.String(), bob.StorageArea) || strings.Contains(r.Body.String(), "/devices/bob-peer/delete") {
		t.Fatalf("admin peer table leaked write actions or wrong category: %d %s", r.Code, r.Body.String())
	}
}
func TestCreateFolderAndDeleteRequiresTypedID(t *testing.T) {
	h, db, c := fixture(t)
	u, e := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var remote *syncthing.Folder
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-API-Key") != "private-key" {
			t.Error("missing daemon authentication")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case r.Method == "GET" && r.URL.Path == "/rest/db/status":
			json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
		case r.Method == "GET" && r.URL.Path == "/rest/config/folders":
			if remote == nil {
				json.NewEncoder(w).Encode([]syncthing.Folder{})
			} else {
				json.NewEncoder(w).Encode([]syncthing.Folder{*remote})
			}
		case r.Method == "POST" && r.URL.Path == "/rest/config/folders":
			var f syncthing.Folder
			if e := json.NewDecoder(r.Body).Decode(&f); e != nil {
				t.Error(e)
			}
			remote = &f
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/config/folders/"):
			if remote == nil {
				http.NotFound(w, r)
			} else {
				json.NewEncoder(w).Encode(remote)
			}
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/rest/config/folders/"):
			remote = nil
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, e := syncthing.NewClient(daemon.URL, "private-key", nil)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	files, e := storage.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer files.Close()
	h = web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, files)
	form := url.Values{"csrf": {csrf(t, h, c)}, "id": {"photos-2026"}, "label": {"Private photos"}, "group": {"Family"}, "subpath": {"Private"}}
	r := serve(h, c, "POST", "/folders", form)
	if r.Code != 303 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	folders, e := db.UserFolders(context.Background(), u.ID)
	if e != nil || len(folders) != 1 {
		t.Fatalf("folder not persisted: %v %v", folders, e)
	}
	if folders[0].ID != "photos-2026" || folders[0].Label != "Private photos" || folders[0].Group != "Family" || folders[0].Subpath != "Private" {
		t.Fatalf("folder fields not persisted: %+v", folders[0])
	}
	if remote == nil || remote.Path != filepath.Join(root, u.StorageArea, "Private") || remote.Type != "sendreceive" || remote.ID != "photos-2026" || remote.Label != "Private photos" || remote.Group != "Family" || len(remote.Devices) != 1 || remote.Devices[0].DeviceID != "SERVER" {
		t.Fatalf("invalid daemon folder: %+v", remote)
	}
	if _, e = os.Stat(remote.Path); e != nil {
		t.Fatal(e)
	}
	page := serve(h, c, "GET", "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Private photos") || !strings.Contains(page.Body.String(), "Group: Family") || !strings.Contains(page.Body.String(), "Subpath: Private") || strings.Contains(page.Body.String(), u.StorageArea) {
		t.Fatalf("folder details missing or private area exposed: %d %s", page.Code, page.Body.String())
	}
	for _, tc := range []struct {
		id, subpath string
		code        int
	}{
		{"photos-2026", "other", 409},
		{"other-id", "Private", 409},
		{"other-id", "nested/path", 400},
		{"other-id", "../escape", 400},
		{"bad/id", "other", 400},
	} {
		attempt := url.Values{"csrf": {csrf(t, h, c)}, "id": {tc.id}, "label": {"Another label"}, "subpath": {tc.subpath}}
		response := serve(h, c, "POST", "/folders", attempt)
		if response.Code != tc.code {
			t.Errorf("create %q %q: HTTP %d, want %d", tc.id, tc.subpath, response.Code, tc.code)
		}
	}
	form = url.Values{"csrf": {csrf(t, h, c)}, "confirm": {"wrong"}}
	r = serve(h, c, "POST", "/folders/"+folders[0].ID+"/delete", form)
	if r.Code != 400 {
		t.Fatalf("incorrect confirmation: %d", r.Code)
	}
	if remote == nil {
		t.Fatal("wrong confirmation removed daemon folder")
	}
	form.Set("confirm", "photos-2026")
	r = serve(h, c, "POST", "/folders/"+folders[0].ID+"/delete", form)
	if r.Code != 303 {
		t.Fatalf("delete: %d %s", r.Code, r.Body.String())
	}
	if remote != nil {
		t.Fatal("daemon folder remains")
	}
	if _, e = os.Stat(filepath.Join(root, u.StorageArea, "Private")); !os.IsNotExist(e) {
		t.Fatalf("files remain: %v", e)
	}
}

func TestFailedFolderWriteBlocksFurtherChanges(t *testing.T) {
	_, db, c := fixture(t)
	u, e := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if e != nil {
		t.Fatal(e)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/system/status" {
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/rest/config/folders" {
			json.NewEncoder(w).Encode([]syncthing.Folder{})
			return
		}
		http.Error(w, "broken daemon", 500)
	}))
	defer daemon.Close()
	client, e := syncthing.NewClient(daemon.URL, "key", nil)
	if e != nil {
		t.Fatal(e)
	}
	files, e := storage.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer files.Close()
	h := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, files)
	req := url.Values{"csrf": {csrf(t, h, c)}, "id": {"private-id"}, "label": {"Private"}, "subpath": {"private"}}
	r := serve(h, c, "POST", "/folders", req)
	if r.Code != 503 {
		t.Fatalf("failed daemon request: %d", r.Code)
	}
	blocked, _, e := db.WriteStatus(context.Background(), u.ID)
	if e != nil || !blocked {
		t.Fatalf("failure not durable: %t %v", blocked, e)
	}
	req.Set("subpath", "another")
	r = serve(h, c, "POST", "/folders", req)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "contact the operator") {
		t.Fatalf("writes not blocked: %d %s", r.Code, r.Body.String())
	}
}

func TestDashboardOnlyDisplaysOwnedStatus(t *testing.T) {
	h, db, c := fixture(t)
	alice, e := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if e != nil {
		t.Fatal(e)
	}
	bob, e := db.UpsertUser(context.Background(), "https://issuer.example", "bob", "Bob", "b@example", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.CreateFolder(context.Background(), alice.ID, "mine", "Mine"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateFolder(context.Background(), bob.ID, "others", "Other"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateDevice(context.Background(), alice.ID, "my-peer", "My peer"); e != nil {
		t.Fatal(e)
	}
	if e = db.CreateDevice(context.Background(), bob.ID, "other-peer", "Other peer"); e != nil {
		t.Fatal(e)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case "/rest/db/status":
			if r.URL.Query().Get("folder") != "mine" {
				t.Errorf("cross-user status request: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(map[string]any{"state": "idle", "path": "secret-server-path", "foreign": "Other"})
		case "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{"my-peer": map[string]any{"connected": true}, "other-peer": map[string]any{"connected": true, "address": "secret-server-path"}}, "total": map[string]any{"foreign": "Other"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, e := syncthing.NewClient(daemon.URL, "api-key", nil)
	if e != nil {
		t.Fatal(e)
	}
	h = web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, nil)
	r := serve(h, c, "GET", "/", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `<span class="state state--idle">idle</span>`) || strings.Contains(r.Body.String(), "secret-server-path") || strings.Contains(r.Body.String(), "Other peer") {
		t.Fatalf("folder status filtering failed: %d %s", r.Code, r.Body.String())
	}
	r = serve(h, c, "GET", "/?tab=peers", nil)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `<span class="state state--connected">connected</span>`) || strings.Contains(r.Body.String(), "secret-server-path") || strings.Contains(r.Body.String(), "Other peer") {
		t.Fatalf("peer status filtering failed: %d %s", r.Code, r.Body.String())
	}
}

func TestCrossOwnerDeleteAndShareCannotModifyResources(t *testing.T) {
	h, db, c := fixture(t)
	bob, e := db.UpsertUser(context.Background(), "https://issuer.example", "bob", "Bob", "b@example", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.CreateFolder(context.Background(), bob.ID, "bob-folder", "Bob files"); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path   string
		values url.Values
	}{{"/folders/bob-folder/delete", url.Values{"confirm": {"Bob files"}}}, {"/folders/bob-folder/shares", url.Values{"device": {"peer"}, "action": {"add"}}}, {"/folders/bob-folder/shares", url.Values{"device": {"peer"}, "action": {"remove"}}}} {
		tc.values.Set("csrf", csrf(t, h, c))
		r := serve(h, c, "POST", tc.path, tc.values)
		if r.Code != 404 {
			t.Errorf("cross-owner %s: %d %s", tc.path, r.Code, r.Body.String())
		}
	}
	fs, e := db.UserFolders(context.Background(), bob.ID)
	if e != nil || len(fs) != 1 {
		t.Fatalf("other user's resources changed: %v %v", fs, e)
	}
}

func TestOIDCLoginBindsBrowserStatePKCEAndRegistration(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var issuer *httptest.Server
	var nonce, challenge string
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			r.ParseForm()
			v := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(v[:]) != challenge {
				t.Error("PKCE verifier did not match challenge")
			}
			claims := map[string]any{"iss": issuer.URL, "sub": "stable-subject", "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "name": "Alice", "email": "alice@example"}
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "key"})
			payload, _ := json.Marshal(claims)
			input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
			hash := sha256.Sum256([]byte(input))
			sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if e != nil {
				t.Error(e)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "access", "token_type": "Bearer", "id_token": fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(sig))})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	provider, e := oidc.New(context.Background(), issuer.URL, "client", "secret", "https://example.test/callback")
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	handler := web.New(config.Config{BaseURL: "https://example.test", RegistrationEnabled: true}, db, nil, provider, nil)
	start := serve(handler, nil, "GET", "/login", nil)
	if start.Code != 303 {
		t.Fatalf("login start: %d", start.Code)
	}
	flow := start.Result().Cookies()[0]
	if !flow.HttpOnly || !flow.Secure || flow.SameSite != http.SameSiteLaxMode {
		t.Fatalf("insecure OIDC cookie: %+v", flow)
	}
	redirect, e := url.Parse(start.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	nonce, challenge = redirect.Query().Get("nonce"), redirect.Query().Get("code_challenge")
	if nonce == "" || challenge == "" {
		t.Fatal("missing nonce or PKCE")
	}
	bad := serve(handler, flow, "GET", "/callback?state=forged&code=valid", nil)
	if bad.Code != 400 {
		t.Fatalf("accepted forged state: %d", bad.Code)
	}
	if _, e = db.FindUser(context.Background(), issuer.URL, "stable-subject"); e != store.ErrNotFound {
		t.Fatalf("forged login created user: %v", e)
	}
	start = serve(handler, nil, "GET", "/login", nil)
	flow = start.Result().Cookies()[0]
	redirect, _ = url.Parse(start.Header().Get("Location"))
	nonce, challenge = redirect.Query().Get("nonce"), redirect.Query().Get("code_challenge")
	path := "/callback?state=" + url.QueryEscape(redirect.Query().Get("state")) + "&code=valid"
	good := serve(handler, flow, "GET", path, nil)
	if good.Code != 303 {
		t.Fatalf("callback: %d %s", good.Code, good.Body.String())
	}
	session := good.Result().Cookies()[1]
	if session.Name != "session" || !session.HttpOnly || !session.Secure {
		t.Fatalf("insecure session: %+v", session)
	}
	if r := serve(handler, session, "GET", "/", nil); r.Code != 200 || !strings.Contains(r.Body.String(), "Alice") {
		t.Fatalf("login dashboard: %d %s", r.Code, r.Body.String())
	}
	if r := serve(handler, flow, "GET", path, nil); r.Code != 400 {
		t.Fatalf("replayed callback accepted: %d", r.Code)
	}
}

func TestHTTPDeviceAddShareUnshareAndRemove(t *testing.T) {
	_, users, session := fixture(t)
	user, err := users.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files, err := storage.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	const peer = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	const other = "KSBX654-F3TLDXY-SDMH7UN-VMHW7MT-FFQ5VIT-LSCPMDY-XWYBOVY-TO5RSQ3"
	var mu sync.Mutex
	folders := make(map[string]syncthing.Folder)
	devices := make(map[string]syncthing.Device)
	var history []string
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-API-Key") != "daemon-secret" {
			t.Error("daemon request missing credential")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case r.Method == "GET" && r.URL.Path == "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{peer: map[string]bool{"connected": true}, other: map[string]bool{"connected": false}}})
		case r.Method == "GET" && r.URL.Path == "/rest/db/status":
			json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
		case r.Method == "GET" && r.URL.Path == "/rest/config/folders":
			all := make([]syncthing.Folder, 0, len(folders))
			for _, folder := range folders {
				all = append(all, folder)
			}
			json.NewEncoder(w).Encode(all)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/config/folders/"):
			id := strings.TrimPrefix(r.URL.Path, "/rest/config/folders/")
			f, ok := folders[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(f)
		case r.Method == "POST" && r.URL.Path == "/rest/config/folders":
			var f syncthing.Folder
			if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
				t.Error(err)
				http.Error(w, "invalid folder", 400)
				return
			}
			folders[f.ID] = f
			history = append(history, "create folder")
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/rest/config/folders/"):
			var f syncthing.Folder
			if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
				t.Error(err)
				http.Error(w, "invalid folder", 400)
				return
			}
			folders[f.ID] = f
			history = append(history, "update folder")
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/config/devices/"):
			id := strings.TrimPrefix(r.URL.Path, "/rest/config/devices/")
			d, ok := devices[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(d)
		case r.Method == "POST" && r.URL.Path == "/rest/config/devices":
			var d syncthing.Device
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				t.Error(err)
				http.Error(w, "invalid device", 400)
				return
			}
			devices[d.DeviceID] = d
			history = append(history, "create device")
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/rest/config/devices/"):
			id := strings.TrimPrefix(r.URL.Path, "/rest/config/devices/")
			delete(devices, id)
			history = append(history, "delete device")
		default:
			http.Error(w, "unexpected daemon endpoint", 500)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "daemon-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, users, client, nil, files)
	post := func(path string, fields url.Values) *httptest.ResponseRecorder {
		t.Helper()
		fields.Set("csrf", csrf(t, handler, session))
		r := serve(handler, session, "POST", path, fields)
		if r.Code != 303 {
			t.Fatalf("POST %s: %d %s", path, r.Code, r.Body.String())
		}
		return r
	}
	if got := post("/devices", url.Values{"id": {peer}, "name": {"Laptop"}}).Header().Get("Location"); got != "/?tab=peers" {
		t.Fatalf("new peer redirected to %q", got)
	}
	post("/devices", url.Values{"id": {other}, "name": {"Phone"}})
	if got := post("/folders", url.Values{"id": {"notes-id"}, "label": {"Notes"}, "group": {"Projects"}, "subpath": {"notes-dir"}}).Header().Get("Location"); got != "/" {
		t.Fatalf("new folder redirected to %q", got)
	}
	page := serve(handler, session, "GET", "/", nil)
	idMatch := regexp.MustCompile(`action="/folders/([^"]+)/shares"`).FindStringSubmatch(page.Body.String())
	if len(idMatch) != 2 {
		t.Fatalf("folder not visible: %s", page.Body.String())
	}
	folderID := idMatch[1]
	folder := folders[folderID]
	if folder.ID != folderID || folder.Group != "Projects" || folder.Path != filepath.Join(root, user.StorageArea, "notes-dir") || len(folder.Devices) != 1 || folder.Devices[0].DeviceID != "SERVER" {
		t.Fatalf("folder must start unshared: %+v", folder)
	}
	post("/folders/"+folderID+"/shares", url.Values{"device": {peer}, "action": {"add"}})
	folder = folders[folderID]
	if len(folder.Devices) != 2 || folder.Devices[1].DeviceID != peer {
		t.Fatalf("peer was not shared: %+v", folder.Devices)
	}
	page = serve(handler, session, "GET", "/", nil)
	if !strings.Contains(page.Body.String(), `name="device" value="`+peer+`"`) || !strings.Contains(page.Body.String(), `aria-label="Unshare Laptop from Notes"`) || !strings.Contains(page.Body.String(), "Shared with") {
		t.Fatalf("shared peer row missing or no unshare action: %s", page.Body.String())
	}
	post("/folders/"+folderID+"/shares", url.Values{"device": {peer}, "action": {"remove"}})
	page = serve(handler, session, "GET", "/", nil)
	if strings.Contains(page.Body.String(), `aria-label="Unshare Laptop from Notes"`) {
		t.Fatalf("unshared peer still appears in folder share list: %s", page.Body.String())
	}
	folder = folders[folderID]
	if len(folder.Devices) != 1 || folder.Devices[0].DeviceID != "SERVER" {
		t.Fatalf("unshare removed wrong device: %+v", folder.Devices)
	}
	post("/folders/"+folderID+"/shares", url.Values{"device": {peer}, "action": {"add"}})
	page = serve(handler, session, "GET", "/", nil)
	if strings.Contains(page.Body.String(), "Removing unshares:") || !strings.Contains(page.Body.String(), `aria-label="Unshare Laptop from Notes"`) {
		t.Fatalf("folder sharing list or peer display incorrect: %s", page.Body.String())
	}
	if got := post("/devices/"+peer+"/delete", url.Values{}).Header().Get("Location"); got != "/?tab=peers" {
		t.Fatalf("removed peer redirected to %q", got)
	}
	folder = folders[folderID]
	if len(folder.Devices) != 1 || folder.Devices[0].DeviceID != "SERVER" {
		t.Fatalf("device removal left stale share: %+v", folder.Devices)
	}
	if _, ok := devices[peer]; ok {
		t.Fatal("removed device remains in daemon")
	}
	if _, ok := devices[other]; !ok {
		t.Fatal("other peer device was removed")
	}
	if len(history) < 2 || history[len(history)-2] != "update folder" || history[len(history)-1] != "delete device" {
		t.Fatalf("device deleted before shares: %v", history)
	}
	page = serve(handler, session, "GET", "/?tab=peers", nil)
	if strings.Contains(page.Body.String(), "Laptop") || !strings.Contains(page.Body.String(), "Phone") || !strings.Contains(page.Body.String(), `<table class="data-table data-table--peers">`) {
		t.Fatalf("device removal not reflected in peer table: %s", page.Body.String())
	}
}

func TestRegistrationDisabledRejectsNewIdentityButRetainsExisting(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer *httptest.Server
	var nonce string
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			r.ParseForm()
			subject := "unknown"
			if r.Form.Get("code") == "registered" {
				subject = "registered"
			}
			claims := map[string]any{"iss": issuer.URL, "sub": subject, "aud": "client", "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "name": "Updated name", "email": "updated@example.net"}
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "key"})
			payload, _ := json.Marshal(claims)
			input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
			hash := sha256.Sum256([]byte(input))
			sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
			if e != nil {
				t.Error(e)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "access", "token_type": "Bearer", "id_token": input + "." + base64.RawURLEncoding.EncodeToString(sig)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	provider, err := oidc.New(context.Background(), issuer.URL, "client", "secret", "https://example.test/callback")
	if err != nil {
		t.Fatal(err)
	}
	users, err := store.Open(filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer users.Close()
	existing, err := users.UpsertUser(context.Background(), issuer.URL, "registered", "Original name", "original@example.net", true)
	if err != nil {
		t.Fatal(err)
	}
	h := web.New(config.Config{BaseURL: "https://example.test", RegistrationEnabled: false}, users, nil, provider, nil)
	login := func(code string) *httptest.ResponseRecorder {
		t.Helper()
		start := serve(h, nil, "GET", "/login", nil)
		if start.Code != 303 {
			t.Fatalf("login: %d", start.Code)
		}
		flow := start.Result().Cookies()[0]
		redirect, err := url.Parse(start.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		nonce = redirect.Query().Get("nonce")
		callback := "/callback?state=" + url.QueryEscape(redirect.Query().Get("state")) + "&code=" + url.QueryEscape(code)
		return serve(h, flow, "GET", callback, nil)
	}
	denied := login("new")
	if denied.Code != 403 || !strings.Contains(denied.Body.String(), "registration disabled") {
		t.Fatalf("new identity was not denied: %d %s", denied.Code, denied.Body.String())
	}
	for _, cookie := range denied.Result().Cookies() {
		if cookie.Name == "session" && cookie.Value != "" {
			t.Fatal("denied identity received session")
		}
	}
	if _, err = users.FindUser(context.Background(), issuer.URL, "unknown"); err != store.ErrNotFound {
		t.Fatalf("unknown identity was registered: %v", err)
	}
	allowed := login("registered")
	if allowed.Code != 303 {
		t.Fatalf("existing identity was denied: %d %s", allowed.Code, allowed.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range allowed.Result().Cookies() {
		if cookie.Name == "session" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("existing identity received no session")
	}
	page := serve(h, session, "GET", "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Updated name") {
		t.Fatalf("existing user cannot access dashboard: %d %s", page.Code, page.Body.String())
	}
	updated, err := users.FindUser(context.Background(), issuer.URL, "registered")
	if err != nil || updated.ID != existing.ID || updated.StorageArea != existing.StorageArea {
		t.Fatalf("existing identity lost ownership: %+v %v", updated, err)
	}
}
