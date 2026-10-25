package pending_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"syncthing-server/internal/pending"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

func TestScanDismissesUnknownDeviceAndIgnoresUnsolicitedFolder(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.UpsertUser(ctx, "https://id.example", "person", "Person", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, user.ID, "PEER-ID", "Laptop"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	dismissed := map[string]bool{}
	ignored := false
	device := `{"deviceID":"PEER-ID","name":"Laptop","ignoredFolders":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/cluster/pending/devices":
			io.WriteString(w, `{"UNKNOWN-ID":{"name":"Stranger"},"PEER-ID":{"name":"Laptop"}}`)
		case r.Method == "DELETE" && r.URL.Path == "/rest/cluster/pending/devices":
			dismissed[r.URL.Query().Get("device")] = true
		case r.Method == "GET" && r.URL.Path == "/rest/cluster/pending/folders":
			io.WriteString(w, `{"unwanted":{"offeredBy":{"PEER-ID":{"label":"Other folder"}}}}`)
		case r.Method == "GET" && r.URL.Path == "/rest/config/devices/PEER-ID":
			io.WriteString(w, device)
		case r.Method == "PUT" && r.URL.Path == "/rest/config/devices/PEER-ID":
			b, _ := io.ReadAll(r.Body)
			var v struct {
				IgnoredFolders []struct {
					ID string `json:"id"`
				} `json:"ignoredFolders"`
			}
			if err := json.Unmarshal(b, &v); err != nil {
				t.Errorf("decode update: %v", err)
			}
			ignored = len(v.IgnoredFolders) == 1 && v.IgnoredFolders[0].ID == "unwanted"
			device = string(b)
		case r.Method == "DELETE" && r.URL.Path == "/rest/cluster/pending/folders":
			if r.URL.Query().Get("folder") != "unwanted" || r.URL.Query().Get("device") != "PEER-ID" {
				t.Errorf("unexpected dismissal %s", r.URL)
			}
		default:
			t.Errorf("unexpected daemon request: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	daemon, err := syncthing.NewClient(server.URL, "api-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	worker := pending.New(daemon, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := worker.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !dismissed["UNKNOWN-ID"] || dismissed["PEER-ID"] {
		t.Fatalf("wrong device dismissals: %+v", dismissed)
	}
	if !ignored || !strings.Contains(device, "ignoredFolders") {
		t.Fatal("unsolicited offer was not persistently ignored")
	}
}
