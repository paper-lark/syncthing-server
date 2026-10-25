package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestAdminSeesOwnedFolderStatisticsWithoutRawDaemonData(t *testing.T) {
	_, db, cookie := fixture(t)
	alice, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(context.Background(), alice.ID, "owned-folder", "Family photos"); err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/db/status":
			if r.URL.Query().Get("folder") != "owned-folder" {
				t.Errorf("foreign folder queried: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]any{"state": "idle", "globalBytes": 512, "needBytes": 128, "path": "/private/server/path", "foreign": "other-user"})
		case "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test", Admins: []config.Admin{{Issuer: alice.Issuer, Subject: alice.Subject}}}, db, client, nil, nil)
	res := serve(handler, cookie, "GET", "/admin", nil)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "512 bytes") || !strings.Contains(res.Body.String(), "128 bytes remaining") || strings.Contains(res.Body.String(), "/private/server/path") || strings.Contains(res.Body.String(), "other-user") {
		t.Fatalf("statistics missing or raw data leaked: %d %s", res.Code, res.Body.String())
	}
}
