package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestDeviceDeletionRefusesCrossUserDaemonShare(t *testing.T) {
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
	id := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	if err := db.CreateDevice(ctx, alice.ID, id, "Alice peer"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, bob.ID, "bob-folder", "Bob files"); err != nil {
		t.Fatal(err)
	}
	deletions := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/connections":
			json.NewEncoder(w).Encode(map[string]any{"connections": map[string]any{}})
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case "/rest/config/folders":
			json.NewEncoder(w).Encode([]syncthing.Folder{{ID: "bob-folder", Devices: []syncthing.FolderDevice{{DeviceID: "SERVER"}, {DeviceID: id}}}})
		case "/rest/config/devices/" + id:
			if r.Method == http.MethodDelete {
				deletions++
				return
			}
			if deletions > 0 {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(syncthing.Device{DeviceID: id, Name: "Alice peer"})
		default:
			if r.Method == http.MethodDelete && r.URL.Path == "/rest/config/devices/"+id {
				deletions++
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, nil)
	response := serve(handler, cookie, http.MethodPost, "/devices/"+id+"/delete", url.Values{"csrf": {csrf(t, handler, cookie)}})
	if response.Code == http.StatusSeeOther || deletions != 0 {
		t.Fatalf("removed device referenced by another user's folder: %d deletions=%d", response.Code, deletions)
	}
}
