package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"syncthing-server/internal/store"
)

func TestDeviceReservationPreventsCrossUserReplacement(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "app.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	alice, err := db.UpsertUser(ctx, "issuer", "alice", "Alice", "", true)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.UpsertUser(ctx, "issuer", "bob", "Bob", "", true)
	if err != nil {
		t.Fatal(err)
	}
	id := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	op, err := db.BeginOperation(ctx, alice.ID, "create-device", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginOperation(ctx, bob.ID, "create-device", id); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("simultaneous duplicate ID not reserved: %v", err)
	}
	if err := db.CreateDevice(ctx, alice.ID, id, "Laptop", op); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginOperation(ctx, bob.ID, "create-device", id); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("registered ID overwritten after prior operation: %v", err)
	}
}
