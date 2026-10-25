package pending_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"syncthing-server/internal/pending"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

func TestScanPreservesOfferForExistingOwnedShare(t *testing.T) {
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
	for _, step := range []func() error{
		func() error { return db.CreateDevice(ctx, user.ID, "PEER-ID", "Laptop") },
		func() error { return db.CreateFolder(ctx, user.ID, "folder1", "Photos") },
		func() error { return db.SetShare(ctx, user.ID, "folder1", "PEER-ID", true) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/cluster/pending/devices":
			io.WriteString(w, `{}`)
		case "/rest/cluster/pending/folders":
			io.WriteString(w, `{"folder1":{"offeredBy":{"PEER-ID":{"label":"Photos"}}}}`)
		default:
			t.Errorf("unexpected daemon write: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	daemon, err := syncthing.NewClient(server.URL, "key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := pending.New(daemon, db, slog.New(slog.NewTextHandler(io.Discard, nil))).Scan(ctx); err != nil {
		t.Fatal(err)
	}
}
