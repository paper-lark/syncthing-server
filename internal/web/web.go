// Package web serves the ownership-scoped browser interface. No daemon API route is exposed.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"syncthing-server/internal/config"
	"syncthing-server/internal/oidc"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

type app struct {
	cfg     config.Config
	users   *store.Store
	daemon  *syncthing.Client
	oidc    *oidc.Provider
	files   *storage.Storage
	key     [32]byte
	mu      sync.Mutex
	pending map[string]login
}
type login struct {
	state, verifier, nonce string
	expires                time.Time
}

// New constructs a browser-only handler. Its dependencies must be initialized before serving.
func New(cfg config.Config, users *store.Store, daemon *syncthing.Client, oidcProvider *oidc.Provider, files *storage.Storage) http.Handler {
	a := &app{cfg: cfg, users: users, daemon: daemon, oidc: oidcProvider, files: files, pending: make(map[string]login)}
	if _, err := rand.Read(a.key[:]); err != nil {
		panic("web: cannot generate CSRF key: " + err.Error())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /login", a.startLogin)
	mux.HandleFunc("GET /callback", a.callback)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /{$}", a.dashboard)
	mux.HandleFunc("GET /admin", a.admin)
	mux.HandleFunc("POST /folders", a.createFolder)
	mux.HandleFunc("POST /devices", a.createDevice)
	mux.HandleFunc("POST /folders/{id}/shares", a.share)
	mux.HandleFunc("POST /folders/{id}/delete", a.deleteFolder)
	mux.HandleFunc("POST /devices/{id}/delete", a.deleteDevice)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (a *app) secure() bool {
	u, err := url.Parse(a.cfg.BaseURL)
	return err == nil && u.Scheme == "https"
}
func (a *app) cookie(name, value string, age int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, Secure: a.secure(), HttpOnly: true, SameSite: http.SameSiteLaxMode}
}
func (a *app) startLogin(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.Error(w, "login unavailable", http.StatusServiceUnavailable)
		return
	}
	state, e := random()
	if e != nil {
		http.Error(w, "login unavailable", 500)
		return
	}
	verifier, e := oidc.NewVerifier()
	if e != nil {
		http.Error(w, "login unavailable", 500)
		return
	}
	nonce, e := random()
	if e != nil {
		http.Error(w, "login unavailable", 500)
		return
	}
	binding, e := random()
	if e != nil {
		http.Error(w, "login unavailable", 500)
		return
	}
	a.mu.Lock()
	for k, p := range a.pending {
		if time.Now().After(p.expires) {
			delete(a.pending, k)
		}
	}
	a.pending[binding] = login{state, verifier, nonce, time.Now().Add(5 * time.Minute)}
	a.mu.Unlock()
	http.SetCookie(w, a.cookie("oidc_flow", binding, 300))
	http.Redirect(w, r, a.oidc.AuthURL(state, verifier, nonce), http.StatusSeeOther)
}
func (a *app) callback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("oidc_flow")
	if err != nil {
		http.Error(w, "invalid login", 400)
		return
	}
	http.SetCookie(w, a.cookie("oidc_flow", "", -1))
	a.mu.Lock()
	p, ok := a.pending[c.Value]
	delete(a.pending, c.Value)
	a.mu.Unlock()
	if !ok || time.Now().After(p.expires) || r.URL.Query().Get("state") != p.state || p.state == "" || r.URL.Query().Get("code") == "" {
		http.Error(w, "invalid login", 400)
		return
	}
	issuer, subject, name, email, err := a.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), p.verifier, p.nonce)
	if err != nil {
		slog.Error("OIDC login failed", "error", err)
		http.Error(w, "invalid login", 400)
		return
	}
	u, err := a.users.UpsertUser(r.Context(), issuer, subject, name, email, a.cfg.RegistrationEnabled)
	if errors.Is(err, store.ErrRegistrationDisabled) {
		http.Error(w, "registration disabled", 403)
		return
	}
	if err != nil {
		slog.Error("login user failed", "error", err)
		http.Error(w, "login unavailable", 500)
		return
	}
	token, err := random()
	if err != nil {
		http.Error(w, "login unavailable", 500)
		return
	}
	if err = a.users.CreateSession(r.Context(), token, u.ID, time.Now().Add(24*time.Hour)); err != nil {
		slog.Error("create session failed", "error", err)
		http.Error(w, "login unavailable", 500)
		return
	}
	http.SetCookie(w, a.cookie("session", token, 86400))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *app) auth(w http.ResponseWriter, r *http.Request, write bool) (store.User, string, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		a.unauthorized(w, r, write)
		return store.User{}, "", false
	}
	u, err := a.users.SessionUser(r.Context(), c.Value)
	if err != nil {
		a.unauthorized(w, r, write)
		return store.User{}, "", false
	}
	if write {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", 400)
			return store.User{}, "", false
		}
		expected := a.csrf(c.Value)
		if !hmac.Equal([]byte(r.PostForm.Get("csrf")), []byte(expected)) || r.PostForm.Get("csrf") == "" {
			http.Error(w, "invalid CSRF token", 403)
			return store.User{}, "", false
		}
		if origin := r.Header.Get("Origin"); origin != "" && strings.TrimRight(origin, "/") != strings.TrimRight(a.cfg.BaseURL, "/") {
			http.Error(w, "invalid origin", 403)
			return store.User{}, "", false
		}
	}
	return u, c.Value, true
}
func (a *app) unauthorized(w http.ResponseWriter, r *http.Request, write bool) {
	if write {
		http.Error(w, "authentication required", 401)
	} else {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
func (a *app) csrf(token string) string {
	h := hmac.New(sha256.New, a.key[:])
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}
func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	_, token, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	if err := a.users.DeleteSession(r.Context(), token); err != nil {
		http.Error(w, "logout failed", 500)
		return
	}
	http.SetCookie(w, a.cookie("session", "", -1))
	http.Redirect(w, r, "/login", 303)
}

var page = template.Must(template.New("page").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.Title}}</title><style>body{font:16px system-ui;max-width:60rem;margin:2rem auto;padding:0 1rem;color:#202834;background:#fafafa}section{border:1px solid #ced5df;border-radius:8px;background:white;padding:1rem;margin:1rem 0}form{margin:.75rem 0}input,button,select{font:inherit;margin:.25rem;padding:.4rem}small{display:block;color:#425269}nav a{margin-right:1rem}</style></head><body><nav><a href="/">My resources</a>{{if .IsAdmin}}<a href="/admin">Overview</a>{{end}}<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out</button></form></nav><h1>{{.Title}}</h1>{{range .Groups}}<section><h2>{{.Name}}</h2><h3>Folders</h3>{{range .Folders}}<div><strong>{{.Name}}</strong> ({{.ID}}){{if .Status}} — {{.Status}}{{end}}{{if .Stats}} — {{.Stats}}{{end}}{{if $.Editable}}<form method="post" action="/folders/{{.ID}}/shares"><input type="hidden" name="csrf" value="{{$.CSRF}}"><select name="device"><option value="">Choose a device</option>{{range .Devices}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select><button name="action" value="add">Share</button><button name="action" value="remove">Unshare</button></form><form method="post" action="/folders/{{.ID}}/delete"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Type folder name to confirm server-side file deletion <input name="confirm" required></label><small>Server-side files will be permanently deleted; peer copies may remain.</small><button>Delete folder</button></form>{{end}}</div>{{else}}<p>No folders</p>{{end}}<h3>Peer devices</h3>{{range .Devices}}<div><strong>{{.Name}}</strong> ({{.ID}}){{if .Status}} — {{.Status}}{{end}}{{if .Shares}}<small>Removing unshares: {{range .Shares}}{{.}}; {{end}}</small>{{end}}{{if $.Editable}}<form method="post" action="/devices/{{.ID}}/delete"><input type="hidden" name="csrf" value="{{$.CSRF}}"><small>Removing this device unshares it from all your folders; files on the peer are not deleted.</small><button>Remove device</button></form>{{end}}</div>{{else}}<p>No devices</p>{{end}}</section>{{end}}{{if .Editable}}<section><h2>Create folder</h2><form method="post" action="/folders"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Single folder name <input name="name" required></label><button>Create</button></form><h2>Add peer</h2><form method="post" action="/devices"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Device ID <input name="id" required></label><label>Display name <input name="name" required></label><button>Add peer</button></form><p>Server device ID: {{.ServerID}}. Add this server on your peer, then accept folder shares there.</p></section>{{end}}</body></html>`))

type viewFolder struct {
	ID, Name, Status, Stats string
	Devices                 []viewDevice
}
type viewDevice struct {
	ID, Name, Status string
	Shares           []string
}
type group struct {
	Name    string
	Folders []viewFolder
	Devices []viewDevice
	Blocked bool
}
type view struct {
	Title, CSRF, ServerID string
	IsAdmin, Editable     bool
	Groups                []group
}

func (a *app) isAdmin(u store.User) bool {
	for _, v := range a.cfg.Admins {
		if v.Issuer == u.Issuer && v.Subject == u.Subject {
			return true
		}
	}
	return false
}
func (a *app) group(ctx context.Context, u store.User) (group, error) {
	g := group{Name: u.Name}
	var err error
	g.Blocked, _, err = a.users.WriteStatus(ctx, u.ID)
	if err != nil {
		return g, err
	}
	fs, err := a.users.UserFolders(ctx, u.ID)
	if err != nil {
		return g, err
	}
	ds, err := a.users.UserDevices(ctx, u.ID)
	if err != nil {
		return g, err
	}
	var connections struct {
		Connections map[string]struct {
			Connected bool `json:"connected"`
		} `json:"connections"`
	}
	if a.daemon != nil && len(ds) != 0 {
		raw, e := a.daemon.Connections(ctx)
		if e != nil {
			return g, e
		}
		if e = json.Unmarshal(raw, &connections); e != nil {
			return g, e
		}
	}
	for _, d := range ds {
		v := viewDevice{ID: d.ID, Name: d.Name}
		if a.daemon != nil {
			if connections.Connections[d.ID].Connected {
				v.Status = "connected"
			} else {
				v.Status = "disconnected"
			}
		}
		shares, e := a.users.DeviceShares(ctx, u.ID, d.ID)
		if e != nil {
			return g, e
		}
		for _, f := range shares {
			v.Shares = append(v.Shares, f.Name)
		}
		g.Devices = append(g.Devices, v)
	}
	for _, f := range fs {
		v := viewFolder{ID: f.ID, Name: f.Name, Devices: g.Devices}
		if a.daemon != nil {
			raw, e := a.daemon.FolderStatus(ctx, f.ID)
			if e != nil {
				return g, e
			}
			var status struct {
				State       string `json:"state"`
				GlobalBytes *int64 `json:"globalBytes"`
				NeedBytes   *int64 `json:"needBytes"`
			}
			if e = json.Unmarshal(raw, &status); e != nil {
				return g, e
			}
			// Only render known status vocabulary, never arbitrary daemon JSON.
			switch status.State {
			case "idle", "scanning", "syncing", "sync-preparing", "scan-waiting", "sync-waiting", "error":
				v.Status = status.State
			}
			if status.GlobalBytes != nil && status.NeedBytes != nil && *status.GlobalBytes >= 0 && *status.NeedBytes >= 0 {
				v.Stats = fmt.Sprintf("%d bytes, %d bytes remaining", *status.GlobalBytes, *status.NeedBytes)
			}
		}
		g.Folders = append(g.Folders, v)
	}
	return g, nil
}
func (a *app) dashboard(w http.ResponseWriter, r *http.Request) {
	u, token, ok := a.auth(w, r, false)
	if !ok {
		return
	}
	g, err := a.group(r.Context(), u)
	if err != nil {
		http.Error(w, "dashboard unavailable", 500)
		return
	}
	v := view{Title: "My resources", CSRF: a.csrf(token), IsAdmin: a.isAdmin(u), Editable: true, Groups: []group{g}}
	if a.daemon != nil {
		v.ServerID, err = a.daemon.MyID(r.Context())
		if err != nil {
			slog.Error("read server identity failed", "error", err)
			http.Error(w, "dashboard unavailable", 503)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err = page.Execute(w, v); err != nil {
		slog.Error("render dashboard failed", "error", err)
	}
}
func (a *app) admin(w http.ResponseWriter, r *http.Request) {
	u, token, ok := a.auth(w, r, false)
	if !ok {
		return
	}
	if !a.isAdmin(u) {
		http.Error(w, "forbidden", 403)
		return
	}
	all, err := a.users.AllUsers(r.Context())
	if err != nil {
		http.Error(w, "overview unavailable", 500)
		return
	}
	v := view{Title: "Overview", CSRF: a.csrf(token), IsAdmin: true}
	for _, user := range all {
		g, e := a.group(r.Context(), user)
		if e != nil {
			http.Error(w, "overview unavailable", 500)
			return
		}
		v.Groups = append(v.Groups, g)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err = page.Execute(w, v); err != nil {
		slog.Error("render overview failed", "error", err)
	}
}
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrBlocked):
		http.Error(w, "Changes are blocked; contact the operator.", 409)
	case errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrNotFound):
		http.Error(w, "resource not found", 404)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "resource already exists", 409)
	default:
		slog.Error("web operation failed", "error", err)
		http.Error(w, "operation failed; contact the operator if changes remain blocked", 503)
	}
}
func (a *app) operation(w http.ResponseWriter, r *http.Request, u store.User, kind, id string, fn func(string) error) {
	unlock := a.users.LockChanges()
	defer unlock()
	op, err := a.users.BeginOperation(r.Context(), u.ID, kind, id)
	if err != nil {
		fail(w, err)
		return
	}
	if err = fn(op); err != nil {
		if e := a.users.FailOperation(context.WithoutCancel(r.Context()), u.ID, op, err.Error()); e != nil {
			slog.Error("failed to record blocked operation", "error", e)
		}
		fail(w, err)
		return
	}
	if err = a.users.CompleteOperation(r.Context(), u.ID, op); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/", 303)
}
func validID(id string) bool { return syncthing.ValidDeviceID(id) }
func (a *app) createFolder(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	name := r.PostForm.Get("name")
	if !storage.ValidName(name) {
		http.Error(w, "invalid folder name", 400)
		return
	}
	existing, e := a.users.UserFolders(r.Context(), u.ID)
	if e != nil {
		fail(w, e)
		return
	}
	for _, f := range existing {
		if f.Name == name {
			fail(w, store.ErrConflict)
			return
		}
	}
	id, e := random()
	if e != nil {
		fail(w, e)
		return
	}
	id = "folder-" + strings.ReplaceAll(id, "-", "_")
	a.operation(w, r, u, "create-folder", id, func(op string) error {
		if a.files == nil || a.daemon == nil {
			return errors.New("service unavailable")
		}
		serverID, e := a.daemon.MyID(r.Context())
		if e != nil {
			return e
		}
		if serverID == "" {
			return errors.New("server has no device identity")
		}
		path, e := a.files.Create(u.StorageArea, name)
		if e != nil {
			return e
		}
		e = a.daemon.CreateFolder(r.Context(), syncthing.Folder{ID: id, Label: name, Path: path, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: serverID}}})
		if e != nil {
			return e
		}
		got, e := a.daemon.Folder(r.Context(), id)
		if e != nil {
			return e
		}
		if got.ID != id || got.Path != path || got.Type != "sendreceive" || len(got.Devices) != 1 || got.Devices[0].DeviceID != serverID {
			return errors.New("folder verification failed")
		}
		return a.users.CreateFolder(r.Context(), u.ID, id, name, op)
	})
}
func (a *app) createDevice(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	id := strings.ToUpper(strings.TrimSpace(r.PostForm.Get("id")))
	name := r.PostForm.Get("name")
	if !validID(id) || !storage.ValidName(name) {
		http.Error(w, "invalid device", 400)
		return
	}
	if _, e := a.users.DeviceOwner(r.Context(), id); e == nil {
		fail(w, store.ErrConflict)
		return
	} else if !errors.Is(e, store.ErrNotFound) {
		fail(w, e)
		return
	}
	if a.daemon == nil {
		fail(w, errors.New("daemon unavailable"))
		return
	}
	a.operation(w, r, u, "create-device", id, func(op string) error {
		if _, e := a.daemon.Device(r.Context(), id); e == nil {
			return errors.New("unowned Syncthing peer device")
		} else {
			var response *syncthing.HTTPError
			if !errors.As(e, &response) || response.StatusCode != http.StatusNotFound {
				return e
			}
		}
		if e := a.daemon.CreateDevice(r.Context(), syncthing.Device{DeviceID: id, Name: name}); e != nil {
			return e
		}
		d, e := a.daemon.Device(r.Context(), id)
		if e != nil {
			return e
		}
		if d.DeviceID != id {
			return errors.New("device verification failed")
		}
		return a.users.CreateDevice(r.Context(), u.ID, id, name, op)
	})
}

// ownedFolderConfig verifies the daemon's ownership-relevant fields before a
// browser mutation can read-modify-write its full-power configuration object.
func (a *app) ownedFolderConfig(ctx context.Context, u store.User, owned store.Folder) (syncthing.Folder, error) {
	if a.daemon == nil || a.files == nil {
		return syncthing.Folder{}, errors.New("service unavailable")
	}
	remote, err := a.daemon.Folder(ctx, owned.ID)
	if err != nil {
		return syncthing.Folder{}, err
	}
	path, err := a.files.Path(u.StorageArea, owned.Name)
	if err != nil {
		return syncthing.Folder{}, err
	}
	if remote.ID != owned.ID || remote.Path != path || remote.Label != owned.Name || remote.Type != "sendreceive" {
		return syncthing.Folder{}, errors.New("folder configuration drift")
	}
	serverID, err := a.daemon.MyID(ctx)
	if err != nil || serverID == "" {
		return syncthing.Folder{}, errors.New("server identity unavailable")
	}
	shares, err := a.users.FolderShares(ctx, u.ID, owned.ID)
	if err != nil {
		return syncthing.Folder{}, err
	}
	expected := map[string]bool{serverID: true}
	for _, share := range shares {
		expected[share.ID] = true
	}
	if len(remote.Devices) != len(expected) {
		return syncthing.Folder{}, errors.New("folder sharing drift")
	}
	for _, device := range remote.Devices {
		if !expected[device.DeviceID] {
			return syncthing.Folder{}, errors.New("folder sharing drift")
		}
		delete(expected, device.DeviceID)
	}
	if len(expected) != 0 {
		return syncthing.Folder{}, errors.New("folder sharing drift")
	}
	return remote, nil
}

func (a *app) share(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	device := r.PostForm.Get("device")
	action := r.PostForm.Get("action")
	if action != "add" && action != "remove" {
		http.Error(w, "invalid action", 400)
		return
	}
	if _, e := a.users.Folder(r.Context(), u.ID, id); e != nil {
		fail(w, e)
		return
	}
	if _, e := a.users.Device(r.Context(), u.ID, device); e != nil {
		fail(w, e)
		return
	}
	a.operation(w, r, u, "share-folder", id, func(op string) error {
		owned, e := a.users.Folder(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		f, e := a.ownedFolderConfig(r.Context(), u, owned)
		if e != nil {
			return e
		}
		found := false
		for _, d := range f.Devices {
			if d.DeviceID == device {
				found = true
			}
		}
		if action == "add" && !found {
			f.Devices = append(f.Devices, syncthing.FolderDevice{DeviceID: device})
		}
		if action == "remove" {
			filtered := f.Devices[:0]
			for _, d := range f.Devices {
				if d.DeviceID != device {
					filtered = append(filtered, d)
				}
			}
			f.Devices = filtered
		}
		if e = a.daemon.UpdateFolder(r.Context(), f); e != nil {
			return e
		}
		got, e := a.daemon.Folder(r.Context(), id)
		if e != nil {
			return e
		}
		found = false
		for _, d := range got.Devices {
			if d.DeviceID == device {
				found = true
			}
		}
		if found != (action == "add") {
			return errors.New("share verification failed")
		}
		return a.users.SetShare(r.Context(), u.ID, id, device, action == "add", op)
	})
}
func (a *app) deleteFolder(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	f, e := a.users.Folder(r.Context(), u.ID, id)
	if e != nil {
		fail(w, e)
		return
	}
	if r.PostForm.Get("confirm") != f.Name {
		http.Error(w, "type the folder name to confirm deletion", 400)
		return
	}
	a.operation(w, r, u, "delete-folder", id, func(op string) error {
		if a.daemon == nil || a.files == nil {
			return errors.New("service unavailable")
		}
		if _, e := a.ownedFolderConfig(r.Context(), u, f); e != nil {
			return e
		}
		exists, e := a.files.Exists(u.StorageArea, f.Name)
		if e != nil || !exists {
			return fmt.Errorf("folder storage mismatch: %v", e)
		}
		if e = a.daemon.DeleteFolder(r.Context(), id); e != nil {
			return e
		}
		_, e = a.daemon.Folder(r.Context(), id)
		var he *syncthing.HTTPError
		if !errors.As(e, &he) || he.StatusCode != 404 {
			return errors.New("folder deletion verification failed")
		}
		if e = a.files.Remove(u.StorageArea, f.Name); e != nil {
			return e
		}
		exists, e = a.files.Exists(u.StorageArea, f.Name)
		if e != nil || exists {
			return errors.New("folder removal verification failed")
		}
		return a.users.DeleteFolder(r.Context(), u.ID, id, op)
	})
}
func (a *app) deleteDevice(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, e := a.users.Device(r.Context(), u.ID, id); e != nil {
		fail(w, e)
		return
	}
	a.operation(w, r, u, "delete-device", id, func(op string) error {
		if a.daemon == nil {
			return errors.New("daemon unavailable")
		}
		ownedDevice, e := a.users.Device(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		remoteDevice, e := a.daemon.Device(r.Context(), id)
		if e != nil || remoteDevice.Name != ownedDevice.Name {
			return errors.New("peer device configuration drift")
		}
		folders, e := a.users.DeviceShares(r.Context(), u.ID, id)
		if e != nil {
			return e
		}
		allowed := make(map[string]bool, len(folders))
		for _, folder := range folders {
			allowed[folder.ID] = true
		}
		all, e := a.daemon.Folders(r.Context())
		if e != nil {
			return e
		}
		for _, folder := range all {
			for _, share := range folder.Devices {
				if share.DeviceID != id || allowed[folder.ID] {
					continue
				}
				if owner, err := a.users.FolderOwner(r.Context(), folder.ID); err == nil {
					if owner.UserID != u.ID {
						if err := a.users.BlockUser(r.Context(), owner.UserID, "peer referenced by another user's folder"); err != nil {
							return err
						}
					}
				} else if errors.Is(err, store.ErrNotFound) {
					if err := a.users.BlockAllWrites(r.Context(), "unowned folder references peer device"); err != nil {
						return err
					}
				} else {
					return err
				}
				return errors.New("peer device referenced by unregistered folder share")
			}
		}
		for _, owned := range folders {
			f, e := a.ownedFolderConfig(r.Context(), u, owned)
			if e != nil {
				return e
			}
			filtered := f.Devices[:0]
			for _, d := range f.Devices {
				if d.DeviceID != id {
					filtered = append(filtered, d)
				}
			}
			f.Devices = filtered
			if e = a.daemon.UpdateFolder(r.Context(), f); e != nil {
				return e
			}
			got, e := a.daemon.Folder(r.Context(), f.ID)
			if e != nil {
				return e
			}
			for _, d := range got.Devices {
				if d.DeviceID == id {
					return errors.New("unshare verification failed")
				}
			}
		}
		if e = a.daemon.DeleteDevice(r.Context(), id); e != nil {
			return e
		}
		_, e = a.daemon.Device(r.Context(), id)
		var he *syncthing.HTTPError
		if !errors.As(e, &he) || he.StatusCode != 404 {
			return errors.New("device deletion verification failed")
		}
		return a.users.DeleteDevice(r.Context(), u.ID, id, op)
	})
}
