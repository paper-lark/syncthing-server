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

func TestCheckFolderDetailsDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*syncthing.Folder)
		want   bool
	}{
		{"unchanged", func(*syncthing.Folder) {}, false},
		{"label", func(f *syncthing.Folder) { f.Label = "Other" }, true},
		{"group", func(f *syncthing.Folder) { f.Group = "Other" }, true},
		{"path", func(f *syncthing.Folder) { f.Path = "/other/path" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			path, err := files.Create(user.StorageArea, "private")
			if err != nil {
				t.Fatal(err)
			}
			if err := db.CreateFolderDetails(ctx, store.Folder{ID: "folder-1", UserID: user.ID, Label: "Photos", Group: "Family", Subpath: "private"}); err != nil {
				t.Fatal(err)
			}
			folder := syncthing.Folder{ID: "folder-1", Label: "Photos", Group: "Family", Path: path, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: "SERVER"}}}
			tc.change(&folder)
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/config/folders":
					json.NewEncoder(w).Encode([]syncthing.Folder{folder})
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
			err = checker.Check(ctx)
			if (err != nil) != tc.want {
				t.Fatalf("drift check: %v, expected drift: %t", err, tc.want)
			}
			blocked, _, err := db.WriteStatus(ctx, user.ID)
			if err != nil || blocked != tc.want {
				t.Fatalf("write block: %t %v, expected %t", blocked, err, tc.want)
			}
		})
	}
}
