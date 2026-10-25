package reconcile_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"syncthing-server/internal/reconcile"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

func TestCheckAllowsSyncedFilesButBlocksMissingFolderDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	files, err := storage.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "app.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.UpsertUser(ctx, "https://id.example", "subject", "User", "", true)
	if err != nil {
		t.Fatal(err)
	}
	p, err := files.Create(user.StorageArea, "photos")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, user.ID, "folder1", "photos"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/status":
			io.WriteString(w, `{"myID":"SERVER"}`)
		case "/rest/config/folders":
			fmt.Fprintf(w, `[{"id":"folder1","label":"photos","path":%q,"type":"sendreceive","devices":[]}]`, p)
		case "/rest/config/devices":
			io.WriteString(w, `[{"deviceID":"SERVER"}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	daemon, err := syncthing.NewClient(server.URL, "key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	checker := reconcile.New(db, daemon, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := os.WriteFile(filepath.Join(p, "photo.jpg"), []byte("synced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(ctx); err != nil {
		t.Fatalf("ordinary synced file treated as drift: %v", err)
	}
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(ctx); err == nil {
		t.Fatal("missing folder was not reported")
	}
	blocked, _, err := db.WriteStatus(ctx, user.ID)
	if err != nil || !blocked {
		t.Fatalf("user writes not blocked: blocked=%v err=%v", blocked, err)
	}
}
