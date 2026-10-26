package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"syncthing-server/internal/store"
)

func TestFolderDetailsAndSubpathUniqueness(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	alice, bob := user(t, db, "alice"), user(t, db, "bob")
	first := store.Folder{ID: "first", UserID: alice.ID, Label: "Photos", Group: "Family", Subpath: "photos-2026"}
	if err := db.CreateFolderDetails(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolderDetails(ctx, store.Folder{ID: "second", UserID: alice.ID, Label: "Photos", Subpath: "photos-2025"}); err != nil {
		t.Fatalf("same label with different subpath: %v", err)
	}
	if err := db.CreateFolderDetails(ctx, store.Folder{ID: "third", UserID: alice.ID, Label: "Other", Subpath: first.Subpath}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same user's duplicate subpath: %v", err)
	}
	if err := db.CreateFolderDetails(ctx, store.Folder{ID: first.ID, UserID: bob.ID, Label: "Other", Subpath: "different"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("global folder ID collision: %v", err)
	}
	if err := db.CreateFolderDetails(ctx, store.Folder{ID: "bob", UserID: bob.ID, Label: "Photos", Subpath: first.Subpath}); err != nil {
		t.Fatalf("other user's subpath should be independent: %v", err)
	}
	got, err := db.Folder(ctx, alice.ID, first.ID)
	if err != nil || got != first {
		t.Fatalf("folder details: %+v %v", got, err)
	}
}

func TestLegacyFolderMigrationPreservesOwnershipAndShares(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, issuer TEXT NOT NULL, subject TEXT NOT NULL, name TEXT NOT NULL, email TEXT NOT NULL, storage_area TEXT NOT NULL UNIQUE, UNIQUE(issuer, subject))`,
		`CREATE TABLE folders (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL, UNIQUE(user_id, name))`,
		`CREATE TABLE devices (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL)`,
		`CREATE TABLE shares (folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE, device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE, PRIMARY KEY(folder_id, device_id))`,
		`CREATE TABLE operations (id TEXT PRIMARY KEY, user_id TEXT NOT NULL UNIQUE REFERENCES users(id), kind TEXT NOT NULL, resource_id TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO users VALUES ('alice','issuer','subject','Alice','','opaque')`,
		`INSERT INTO folders VALUES ('stable-id','alice','Old path')`,
		`INSERT INTO devices VALUES ('peer','alice','Laptop')`,
		`INSERT INTO shares VALUES ('stable-id','peer')`,
		`INSERT INTO operations VALUES ('operation','alice','share-folder','stable-id','pending','')`,
	} {
		if _, err := legacy.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		folder, err := db.Folder(ctx, "alice", "stable-id")
		if err != nil || folder.Label != "Old path" || folder.Subpath != "Old path" || folder.Group != "" {
			t.Fatalf("migrated folder: %+v %v", folder, err)
		}
		shares, err := db.DeviceShares(ctx, "alice", "peer")
		if err != nil || len(shares) != 1 || shares[0] != folder {
			t.Fatalf("migrated shares: %+v %v", shares, err)
		}
		blocked, _, err := db.WriteStatus(ctx, "alice")
		if err != nil || !blocked {
			t.Fatalf("unfinished operation lost: %t %v", blocked, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
