package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestFolderDisplaysOnlyOwnedSharesAndScopedTelemetry(t *testing.T) {
	_, db, cookie := fixture(t)
	ctx := context.Background()
	alice, err := db.FindUser(ctx, "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.UpsertUser(ctx, "https://issuer.example", "bob", "Bob", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []store.Folder{
		{ID: "alice-folder", UserID: alice.ID, Label: "Album", Subpath: "album"},
		{ID: "bob-folder", UserID: bob.ID, Label: "Secret album", Subpath: "secret"},
	} {
		if err := db.CreateFolderDetails(ctx, folder); err != nil {
			t.Fatal(err)
		}
	}
	for _, device := range []struct{ userID, id, name string }{
		{alice.ID, "online-peer", "Laptop"},
		{alice.ID, "offline-peer", "Tablet"},
		{alice.ID, "unshared-peer", "Phone"},
		{alice.ID, "zero-peer", "Spare tablet"},
		{bob.ID, "bob-peer", "Secret device"},
	} {
		if err := db.CreateDevice(ctx, device.userID, device.id, device.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][3]string{{alice.ID, "alice-folder", "online-peer"}, {alice.ID, "alice-folder", "offline-peer"}, {bob.ID, "bob-folder", "bob-peer"}} {
		if err := db.SetShare(ctx, pair[0], pair[1], pair[2], true); err != nil {
			t.Fatal(err)
		}
	}
	var completionQueries []string
	statsRequests := 0
	deviceStatsRequests := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{"online-peer": map[string]bool{"connected": true}, "offline-peer": map[string]bool{"connected": false}, "unshared-peer": map[string]bool{"connected": true}, "zero-peer": map[string]bool{"connected": false}, "bob-peer": map[string]bool{"connected": true}}})
		case "/rest/db/status":
			if r.URL.Query().Get("folder") != "alice-folder" {
				t.Errorf("queried another user's folder: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
		case "/rest/stats/device":
			deviceStatsRequests++
			json.NewEncoder(w).Encode(map[string]any{"online-peer": map[string]any{"lastSeen": "2026-10-26T11:22:33Z", "path": "/private/hidden"}, "offline-peer": map[string]any{"lastSeen": "1970-01-01T00:00:00Z"}, "unshared-peer": map[string]any{"lastSeen": "not-a-time"}, "zero-peer": map[string]any{"lastSeen": "0001-01-01T00:00:00Z"}, "bob-peer": map[string]any{"lastSeen": "2025-01-01T00:00:00Z", "name": "SECRET_MARKER"}})
		case "/rest/stats/folder":
			statsRequests++
			json.NewEncoder(w).Encode(map[string]any{"alice-folder": map[string]any{"lastScan": "2026-10-26T10:15:30Z", "path": "/private/hidden"}, "bob-folder": map[string]any{"lastScan": "2025-01-01T00:00:00Z", "name": "SECRET_MARKER"}})
		case "/rest/db/completion":
			completionQueries = append(completionQueries, r.URL.RawQuery)
			if r.URL.Query().Get("folder") != "alice-folder" || r.URL.Query().Get("device") != "online-peer" {
				t.Errorf("queried unshared or cross-user completion: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]any{"completion": 82.39, "path": "/private/hidden", "foreign": "SECRET_MARKER"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, nil)
	response := serve(handler, cookie, "GET", "/", nil)
	if response.Code != 200 {
		t.Fatalf("folder dashboard: %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, fragment := range []string{"Shared with", "Laptop", "Tablet", "Sync: 82.3% synced", "disconnected", "Server last scan: ", "26 Oct 2026, 10:15 UTC", `datetime="2026-10-26T10:15:30Z"`, "Last seen: ", "26 Oct 2026, 11:22 UTC", `datetime="2026-10-26T11:22:33Z"`, "Last seen: Never seen", "Last seen: Unavailable", `name="device" value="online-peer"`, `name="device" value="offline-peer"`, `aria-label="Unshare Laptop from Album"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("missing owned share data %q", fragment)
		}
	}
	if strings.Count(body, "Server last scan:") != 1 || strings.Count(body, "Last seen: Never seen") != 2 || statsRequests != 1 || deviceStatsRequests != 1 || len(completionQueries) != 1 {
		t.Errorf("telemetry request scope: scans %d, folder stats %d, device stats %d, completion requests %v", strings.Count(body, "Server last scan:"), statsRequests, deviceStatsRequests, completionQueries)
	}
	for _, secret := range []string{"Secret album", "Secret device", "SECRET_MARKER", "/private/hidden", `aria-label="Unshare Phone`} {
		if strings.Contains(body, secret) {
			t.Errorf("unowned or unshared data leaked: %q", secret)
		}
	}
}

func TestMissingPeerTelemetryKeepsFolderVisible(t *testing.T) {
	_, db, cookie := fixture(t)
	ctx := context.Background()
	alice, err := db.FindUser(ctx, "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "photos", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "peer", "Laptop"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetShare(ctx, alice.ID, "photos", "peer", true); err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{"peer": map[string]bool{"connected": true}}})
		case "/rest/db/status":
			json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
		default:
			http.Error(w, "telemetry unavailable", 500)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, nil)
	response := serve(handler, cookie, "GET", "/", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Server last scan: Unavailable") || !strings.Contains(response.Body.String(), "Sync: Unavailable") || !strings.Contains(response.Body.String(), "Laptop") {
		t.Fatalf("telemetry errors hid folder sharing: %d %s", response.Code, response.Body.String())
	}
}
