package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"syncthing-server/internal/store"
)

func TestGlobalDriftBlocksUsersRegisteredAfterDetection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.BlockAllWrites(ctx, "unowned Syncthing folder"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.UpsertUser(ctx, "https://issuer.example", "first", "First", "", true)
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := db.WriteStatus(ctx, user.ID)
	if err != nil || !blocked {
		t.Fatalf("new user not blocked after restart: %v %v", blocked, err)
	}
	if _, err := db.BeginOperation(ctx, user.ID, "create-folder", "folder-1"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("write allowed despite global drift: %v", err)
	}
}
