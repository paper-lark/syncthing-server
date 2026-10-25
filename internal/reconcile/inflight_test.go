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

func TestInFlightFolderDoesNotGloballyBlockOtherUsers(t *testing.T) {
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
	alice, err := db.UpsertUser(ctx, "issuer", "alice", "Alice", "", true)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.UpsertUser(ctx, "issuer", "bob", "Bob", "", true)
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.BeginOperation(ctx, alice.ID, "create-folder", "new-folder")
	if err != nil {
		t.Fatal(err)
	}
	path, err := files.Create(alice.StorageArea, "photos")
	if err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/config/folders":
			json.NewEncoder(w).Encode([]syncthing.Folder{{ID: "new-folder", Label: "photos", Path: path, Type: "sendreceive"}})
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
	if err := reconcile.New(db, client, files, slog.New(slog.NewTextHandler(io.Discard, nil))).Check(ctx); err != nil {
		t.Fatalf("in-flight folder marked unowned: %v", err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "new-folder", "photos", op); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); err != nil {
		t.Fatal(err)
	}
	blocked, _, err := db.WriteStatus(ctx, bob.ID)
	if err != nil || blocked {
		t.Fatalf("unrelated user blocked by in-flight folder: %v %v", blocked, err)
	}
}
