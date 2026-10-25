package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestSharingRefusesForeignDeviceAlreadyOnDaemonFolder(t *testing.T) {
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
	ownID := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	foreignID := "KSBX654-F3TLDXY-SDMH7UN-VMHW7MT-FFQ5VIT-LSCPMDY-XWYBOVY-TO5RSQ3"
	if err := db.CreateDevice(ctx, alice.ID, ownID, "Own"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, bob.ID, foreignID, "Foreign"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "photos", "Photos"); err != nil {
		t.Fatal(err)
	}
	files, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if _, err := files.Create(alice.StorageArea, "Photos"); err != nil {
		t.Fatal(err)
	}
	path, err := files.Path(alice.StorageArea, "Photos")
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{}})
		case "/rest/db/status":
			json.NewEncoder(w).Encode(map[string]string{"state": "idle"})
		case "/rest/config/folders/photos":
			if r.Method == http.MethodPut {
				writes++
			}
			json.NewEncoder(w).Encode(syncthing.Folder{ID: "photos", Label: "Photos", Path: path, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: "SERVER"}, {DeviceID: foreignID}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, files)
	response := serve(handler, cookie, http.MethodPost, "/folders/photos/shares", url.Values{"csrf": {csrf(t, handler, cookie)}, "device": {ownID}, "action": {"add"}})
	if response.Code == http.StatusSeeOther || writes != 0 {
		t.Fatalf("foreign share was preserved by write: %d writes=%d", response.Code, writes)
	}
	blocked, _, err := db.WriteStatus(ctx, alice.ID)
	if err != nil || !blocked {
		t.Fatalf("folder owner was not blocked: %v %v", blocked, err)
	}
}
