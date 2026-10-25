package reconcile_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"syncthing-server/internal/reconcile"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

func TestCheckBlocksOutOfBandFolderTypeChange(t *testing.T) {
	ctx := context.Background()
	files, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.UpsertUser(ctx, "https://id.example", "sub", "User", "", true)
	if err != nil {
		t.Fatal(err)
	}
	path, err := files.Create(user.StorageArea, "photos")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, user.ID, "folder-1", "photos"); err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/config/folders":
			json.NewEncoder(w).Encode([]syncthing.Folder{{ID: "folder-1", Label: "photos", Path: path, Type: "sendonly"}})
		case "/rest/config/devices":
			io.WriteString(w, `[{"deviceID":"SERVER"}]`)
		case "/rest/system/status":
			io.WriteString(w, `{"myID":"SERVER"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	checker := reconcile.New(db, client, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := checker.Check(ctx); err == nil {
		t.Fatal("accepted daemon folder changed to send-only")
	}
	blocked, _, err := db.WriteStatus(ctx, user.ID)
	if err != nil || !blocked {
		t.Fatalf("type drift did not block writes: %v %v", blocked, err)
	}
}
