package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestDuplicateOwnedFolderDoesNotBlockUser(t *testing.T) {
	handler, db, cookie := fixture(t)
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(context.Background(), user.ID, "existing-id", "Photos"); err != nil {
		t.Fatal(err)
	}
	response := serve(handler, cookie, "POST", "/folders", url.Values{"csrf": {csrf(t, handler, cookie)}, "id": {"new-id"}, "label": {"Another label"}, "subpath": {"Photos"}})
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate folder: %d %s", response.Code, response.Body.String())
	}
	blocked, _, err := db.WriteStatus(context.Background(), user.ID)
	if err != nil || blocked {
		t.Fatalf("ordinary duplicate blocked user: %v %v", blocked, err)
	}
}

func TestBlockedWritesDoNotRenderPersistentBanner(t *testing.T) {
	handler, db, cookie := fixture(t)
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BlockUser(context.Background(), user.ID, "storage mismatch"); err != nil {
		t.Fatal(err)
	}
	response := serve(handler, cookie, "GET", "/", nil)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Changes are blocked") || strings.Contains(response.Body.String(), "storage mismatch") {
		t.Fatalf("persistent drift warning shown: %d %s", response.Code, response.Body.String())
	}
}

func TestExistingUnownedDeviceCannotBeOverwritten(t *testing.T) {
	_, db, cookie := fixture(t)
	id := "KSBX654-F3TLDXY-SDMH7UN-VMHW7MT-FFQ5VIT-LSCPMDY-XWYBOVY-TO5RSQ3"
	writes := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case r.Method == "GET" && r.URL.Path == "/rest/config/devices/"+id:
			json.NewEncoder(w).Encode(map[string]string{"deviceID": id, "name": "Unowned"})
		case r.Method == "POST" && r.URL.Path == "/rest/config/devices":
			writes++
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
	response := serve(handler, cookie, "POST", "/devices", url.Values{"csrf": {csrf(t, handler, cookie)}, "id": {id}, "name": {"My peer"}})
	if response.Code == http.StatusSeeOther || writes != 0 {
		t.Fatalf("overwrote existing daemon peer: status=%d writes=%d", response.Code, writes)
	}
	if _, err := db.DeviceOwner(context.Background(), id); err != store.ErrNotFound {
		t.Fatalf("adopted unowned peer: %v", err)
	}
}
