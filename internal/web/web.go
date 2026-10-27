// Package web serves the ownership-scoped browser interface. No daemon API route is exposed.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	mux.HandleFunc("GET /assets/dialogs.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(dialogsJS)
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
	mux.HandleFunc("POST /admin/folders/{id}/delete", a.adminDeleteFolder)
	mux.HandleFunc("POST /devices/{id}/delete", a.deleteDevice)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
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
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host, a.cfg.BaseURL) {
			http.Error(w, "invalid origin", 403)
			return store.User{}, "", false
		}
	}
	return u, c.Value, true
}

func sameOrigin(origin, host, baseURL string) bool {
	given, err := url.Parse(origin)
	if err != nil || given.Host == "" || given.User != nil || given.Path != "" || given.RawQuery != "" || given.Fragment != "" {
		return false
	}
	base, err := url.Parse(baseURL)
	if err != nil || given.Scheme != base.Scheme {
		return false
	}
	return strings.EqualFold(given.Host, host) || strings.EqualFold(given.Host, base.Host)
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

//go:embed page.html
var pageHTML string

//go:embed dialogs.js
var dialogsJS []byte

var page = template.Must(template.New("page").Parse(pageHTML))

type viewFolder struct {
	ID, Label, FolderGroup, Subpath, Status, ServerCompletion, FolderTotal string
	LastScan, LastScanTime                                                 string
	Devices                                                                []viewDevice
	Shared                                                                 []viewSharedDevice
}
type viewSharedDevice struct {
	ID, Name, Status, Completion string
}
type viewDevice struct {
	ID, Name, Status, LastSeen, LastSeenTime string
}
type group struct {
	Name    string
	Folders []viewFolder
	Devices []viewDevice
	Blocked bool
}
type view struct {
	Title, CSRF, ServerID, Tab, UserName string
	IsAdmin, Editable                    bool
	Groups                               []group
}

func resourceTab(r *http.Request) string {
	if r.URL.Query().Get("tab") == "peers" {
		return "peers"
	}
	return "folders"
}

func (a *app) isAdmin(u store.User) bool {
	for _, v := range a.cfg.Admins {
		if v.Issuer == u.Issuer && v.Subject == u.Subject {
			return true
		}
	}
	return false
}

func folderLastScan(raw json.RawMessage) (string, string) {
	var stats struct {
		LastScan string `json:"lastScan"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil || stats.LastScan == "" {
		return "Unavailable", ""
	}
	when, err := time.Parse(time.RFC3339Nano, stats.LastScan)
	if err != nil {
		return "Unavailable", ""
	}
	if when.IsZero() {
		return "Not scanned yet", ""
	}
	return when.UTC().Format("02 Jan 2006, 15:04 UTC"), when.UTC().Format(time.RFC3339)
}

func deviceLastSeen(raw json.RawMessage) (string, string) {
	var stats struct {
		LastSeen string `json:"lastSeen"`
	}
	if err := json.Unmarshal(raw, &stats); err != nil || stats.LastSeen == "" {
		return "Unavailable", ""
	}
	when, err := time.Parse(time.RFC3339Nano, stats.LastSeen)
	if err != nil {
		return "Unavailable", ""
	}
	if !when.After(time.Unix(0, 0)) {
		return "Never seen", ""
	}
	return when.UTC().Format("02 Jan 2006, 15:04 UTC"), when.UTC().Format(time.RFC3339)
}

func peerCompletion(raw json.RawMessage) string {
	var result struct {
		Completion *float64 `json:"completion"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Completion == nil || math.IsNaN(*result.Completion) || *result.Completion < 0 || *result.Completion > 100 {
		return "Unavailable"
	}
	return fmt.Sprintf("%.1f%% synced", math.Floor(*result.Completion*10)/10)
}

func (a *app) group(ctx context.Context, u store.User, tab string) (group, error) {
	g := group{Name: u.Name}
	var err error
	g.Blocked, _, err = a.users.WriteStatus(ctx, u.ID)
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
		if raw, e := a.daemon.Connections(ctx); e == nil {
			_ = json.Unmarshal(raw, &connections)
		}
	}
	connectionStatus := func(id string) string {
		if info, ok := connections.Connections[id]; ok {
			if info.Connected {
				return "connected"
			}
			return "disconnected"
		}
		return "unknown"
	}
	var deviceStats map[string]json.RawMessage
	if tab == "peers" && a.daemon != nil && len(ds) != 0 {
		statsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		deviceStats, _ = a.daemon.DeviceStats(statsCtx)
		cancel()
	}
	for _, d := range ds {
		v := viewDevice{ID: d.ID, Name: d.Name, Status: connectionStatus(d.ID)}
		v.LastSeen, v.LastSeenTime = deviceLastSeen(deviceStats[d.ID])
		g.Devices = append(g.Devices, v)
	}
	if tab == "peers" {
		return g, nil
	}
	fs, err := a.users.UserFolders(ctx, u.ID)
	if err != nil {
		return g, err
	}
	var folderStats map[string]json.RawMessage
	if a.daemon != nil && len(fs) != 0 {
		statsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		folderStats, _ = a.daemon.FolderStats(statsCtx)
		cancel()
	}
	for _, f := range fs {
		v := viewFolder{ID: f.ID, Label: f.Label, FolderGroup: f.Group, Subpath: f.Subpath, ServerCompletion: "Unavailable"}
		v.LastScan, v.LastScanTime = folderLastScan(folderStats[f.ID])
		if a.daemon != nil {
			raw, e := a.daemon.FolderStatus(ctx, f.ID)
			if e != nil {
				return g, e
			}
			var status struct {
				State       string `json:"state"`
				GlobalBytes *int64 `json:"globalBytes"`
			}
			if e = json.Unmarshal(raw, &status); e != nil {
				return g, e
			}
			if status.GlobalBytes != nil && *status.GlobalBytes >= 0 {
				v.FolderTotal = fmt.Sprintf("%d bytes", *status.GlobalBytes)
			}
			// Only render known status vocabulary, never arbitrary daemon JSON.
			switch status.State {
			case "idle", "scanning", "syncing", "sync-preparing", "scan-waiting", "sync-waiting", "error":
				v.Status = status.State
			}
			completionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			completion, e := a.daemon.Completion(completionCtx, f.ID, "")
			cancel()
			if e == nil {
				v.ServerCompletion = peerCompletion(completion)
			}
		}
		shares, e := a.users.FolderShares(ctx, u.ID, f.ID)
		if e != nil {
			return g, e
		}
		sharedIDs := make(map[string]bool, len(shares))
		for _, peer := range shares {
			sharedIDs[peer.ID] = true
			shared := viewSharedDevice{ID: peer.ID, Name: peer.Name, Status: connectionStatus(peer.ID), Completion: "Unavailable"}
			if shared.Status == "connected" && a.daemon != nil {
				completionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				raw, e := a.daemon.Completion(completionCtx, f.ID, peer.ID)
				cancel()
				if e == nil {
					shared.Completion = peerCompletion(raw)
				}
			}
			v.Shared = append(v.Shared, shared)
		}
		for _, device := range g.Devices {
			if !sharedIDs[device.ID] {
				v.Devices = append(v.Devices, device)
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
	tab := resourceTab(r)
	g, err := a.group(r.Context(), u, tab)
	if err != nil {
		http.Error(w, "dashboard unavailable", 500)
		return
	}
	v := view{Title: "My resources", CSRF: a.csrf(token), Tab: tab, UserName: u.Name, IsAdmin: a.isAdmin(u), Editable: true, Groups: []group{g}}
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
	tab := resourceTab(r)
	v := view{Title: "Overview", CSRF: a.csrf(token), Tab: tab, UserName: u.Name, IsAdmin: true}
	for _, user := range all {
		g, e := a.group(r.Context(), user, tab)
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
func (a *app) operation(w http.ResponseWriter, r *http.Request, u store.User, kind, id string, fn func(string) error, preflight ...func() error) {
	unlock := a.users.LockChanges()
	defer unlock()
	for _, check := range preflight {
		if err := check(); err != nil {
			fail(w, err)
			return
		}
	}
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
	target := "/"
	if kind == "create-device" || kind == "delete-device" {
		target = "/?tab=peers"
	} else if kind == "delete-folder" && strings.HasPrefix(r.URL.Path, "/admin/") {
		target = "/admin?tab=folders"
	}
	http.Redirect(w, r, target, 303)
}
func validID(id string) bool { return syncthing.ValidDeviceID(id) }

var folderIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validFolderText(s string, required bool) bool {
	return (!required || s != "") && len(s) <= 255 && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func (a *app) createFolder(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	id := r.PostForm.Get("id")
	label := strings.TrimSpace(r.PostForm.Get("label"))
	folderGroup := strings.TrimSpace(r.PostForm.Get("group"))
	subpath := r.PostForm.Get("subpath")
	if !folderIDPattern.MatchString(id) || !validFolderText(label, true) || !validFolderText(folderGroup, false) || !storage.ValidName(subpath) {
		http.Error(w, "invalid folder ID, label, group or subpath", 400)
		return
	}
	a.operation(w, r, u, "create-folder", id, func(op string) error {
		serverID, e := a.daemon.MyID(r.Context())
		if e != nil {
			return e
		}
		if serverID == "" {
			return errors.New("server has no device identity")
		}
		path, e := a.files.Create(u.StorageArea, subpath)
		if e != nil {
			return e
		}
		e = a.daemon.CreateFolder(r.Context(), syncthing.Folder{ID: id, Label: label, Group: folderGroup, Path: path, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: serverID}}})
		if e != nil {
			return e
		}
		got, e := a.daemon.Folder(r.Context(), id)
		if e != nil {
			return e
		}
		if got.ID != id || got.Label != label || got.Group != folderGroup || got.Path != path || got.Type != "sendreceive" || len(got.Devices) != 1 || got.Devices[0].DeviceID != serverID {
			return errors.New("folder verification failed")
		}
		return a.users.CreateFolderDetails(r.Context(), store.Folder{ID: id, UserID: u.ID, Label: label, Group: folderGroup, Subpath: subpath}, op)
	}, func() error {
		folders, e := a.users.UserFolders(r.Context(), u.ID)
		if e != nil {
			return e
		}
		for _, f := range folders {
			if f.Subpath == subpath {
				return store.ErrConflict
			}
		}
		if a.files == nil || a.daemon == nil {
			return errors.New("service unavailable")
		}
		exists, e := a.files.Exists(u.StorageArea, subpath)
		if e != nil {
			return e
		}
		if exists {
			return store.ErrConflict
		}
		remote, e := a.daemon.Folders(r.Context())
		if e != nil {
			return e
		}
		for _, f := range remote {
			if f.ID == id {
				return store.ErrConflict
			}
		}
		return nil
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
	path, err := a.files.Path(u.StorageArea, owned.Subpath)
	if err != nil {
		return syncthing.Folder{}, err
	}
	if remote.ID != owned.ID || remote.Path != path || remote.Label != owned.Label || remote.Group != owned.Group || remote.Type != "sendreceive" {
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
	f, err := a.users.Folder(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	a.deleteFolderFor(w, r, u, f)
}

func (a *app) adminDeleteFolder(w http.ResponseWriter, r *http.Request) {
	admin, _, ok := a.auth(w, r, true)
	if !ok {
		return
	}
	if !a.isAdmin(admin) {
		http.Error(w, "forbidden", 403)
		return
	}
	f, err := a.users.FolderOwner(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	owner, err := a.users.User(r.Context(), f.UserID)
	if err != nil {
		fail(w, err)
		return
	}
	a.deleteFolderFor(w, r, owner, f)
}

func (a *app) deleteFolderFor(w http.ResponseWriter, r *http.Request, u store.User, f store.Folder) {
	id := f.ID
	if r.PostForm.Get("confirm") != id {
		http.Error(w, "type the folder ID to confirm deletion", 400)
		return
	}
	a.operation(w, r, u, "delete-folder", id, func(op string) error {
		if a.daemon == nil || a.files == nil {
			return errors.New("service unavailable")
		}
		if _, e := a.ownedFolderConfig(r.Context(), u, f); e != nil {
			return e
		}
		exists, e := a.files.Exists(u.StorageArea, f.Subpath)
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
		if e = a.files.Remove(u.StorageArea, f.Subpath); e != nil {
			return e
		}
		exists, e = a.files.Exists(u.StorageArea, f.Subpath)
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
