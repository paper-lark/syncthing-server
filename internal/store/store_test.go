package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"syncthing-server/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "users.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func user(t *testing.T, db *store.Store, subject string) store.User {
	t.Helper()
	u, err := db.UpsertUser(context.Background(), "https://issuer.example", subject, subject, subject+"@example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestIdentityIsStableAndRegistrationCanBeDisabled(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	first := user(t, db, "alice")
	if first.ID == "" || first.StorageArea == "" || first.StorageArea == "alice" {
		t.Fatalf("expected opaque stable account: %+v", first)
	}
	updated, err := db.UpsertUser(ctx, first.Issuer, first.Subject, "New name", "changed@example.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != updated.ID || first.StorageArea != updated.StorageArea || updated.Name != "New name" {
		t.Fatalf("profile changed identity or storage: %+v %+v", first, updated)
	}
	if _, err = db.UpsertUser(ctx, "https://other.example", "alice", "X", "x@example.com", false); !errors.Is(err, store.ErrRegistrationDisabled) {
		t.Fatalf("unknown issuer accepted: %v", err)
	}
	if _, err = db.FindUser(ctx, "https://other.example", "alice"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rejected identity persisted: %v", err)
	}
}

func TestOwnershipAndShareFiltering(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	alice, bob := user(t, db, "alice"), user(t, db, "bob")
	if err := db.CreateFolder(ctx, alice.ID, "stable-id-1", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, bob.ID, "stable-id-2", "Photos"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "PEER-ID-1", "Laptop"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, bob.ID, "PEER-ID-2", "Phone"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, bob.ID, "PEER-ID-1", "Stolen"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate ownership transferred: %v", err)
	}
	if err := db.CreateFolder(ctx, bob.ID, "stable-id-1", "Other"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate folder transferred: %v", err)
	}
	if err := db.SetShare(ctx, alice.ID, "stable-id-1", "PEER-ID-2", true); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("cross-owner share accepted: %v", err)
	}
	if err := db.SetShare(ctx, bob.ID, "stable-id-1", "PEER-ID-2", true); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("cross-owner folder share accepted: %v", err)
	}
	if err := db.SetShare(ctx, alice.ID, "stable-id-1", "PEER-ID-1", true); err != nil {
		t.Fatal(err)
	}
	folders, err := db.UserFolders(ctx, alice.ID)
	if err != nil || len(folders) != 1 || folders[0].ID != "stable-id-1" {
		t.Fatalf("alice folders: %+v %v", folders, err)
	}
	devices, err := db.UserDevices(ctx, alice.ID)
	if err != nil || len(devices) != 1 || devices[0].ID != "PEER-ID-1" {
		t.Fatalf("alice devices: %+v %v", devices, err)
	}
	shares, err := db.DeviceShares(ctx, alice.ID, "PEER-ID-1")
	if err != nil || len(shares) != 1 || shares[0].ID != "stable-id-1" {
		t.Fatalf("device shares: %+v %v", shares, err)
	}
	if _, err := db.Folder(ctx, bob.ID, "stable-id-1"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("cross-user folder readable: %v", err)
	}
	if _, err := db.Device(ctx, bob.ID, "PEER-ID-1"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("cross-user device readable: %v", err)
	}
	users, err := db.AllUsers(ctx)
	if err != nil || len(users) != 2 {
		t.Fatalf("admin listing: %+v %v", users, err)
	}
	if err := db.DeleteDevice(ctx, alice.ID, "PEER-ID-1"); err != nil {
		t.Fatal(err)
	}
	folderShares, err := db.FolderShares(ctx, alice.ID, "stable-id-1")
	if err != nil || len(folderShares) != 0 {
		t.Fatalf("stale share after device deletion: %+v %v", folderShares, err)
	}
}

func TestIncompleteOperationsBlockWritesAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "durable.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	alice := user(t, db, "alice")
	op, err := db.BeginOperation(ctx, alice.ID, "create-folder", "folder-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginOperation(ctx, alice.ID, "create-device", "device-one"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("concurrent operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blocked, reason, err := db.WriteStatus(ctx, alice.ID)
	if err != nil || !blocked || reason == "" {
		t.Fatalf("lost incomplete operation: %t %q %v", blocked, reason, err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "second-folder", "Second"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("blocked user wrote: %v", err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("interrupted operation was automatically completed: %v", err)
	}
	if err := db.ResolveUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	blocked, _, err = db.WriteStatus(ctx, alice.ID)
	if err != nil || blocked {
		t.Fatalf("reconciled user still blocked: %t %v", blocked, err)
	}
	op, err = db.BeginOperation(ctx, alice.ID, "delete-folder", "folder-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FailOperation(ctx, alice.ID, op, "filesystem deletion failed"); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("blocked operation silently completed: %v", err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "dev", "Laptop"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("blocked user wrote: %v", err)
	}
}

func TestOperationTokenScopesWritesAndDriftBlocksOnlyAffectedUser(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	alice, bob := user(t, db, "alice"), user(t, db, "bob")
	op, err := db.BeginOperation(ctx, alice.ID, "add-device", "ALICE-PEER")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "ALICE-PEER", "Laptop"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("write without operation token: %v", err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "ALICE-PEER", "Laptop", "wrong-token"); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("write with wrong operation token: %v", err)
	}
	if err := db.CreateDevice(ctx, alice.ID, "ALICE-PEER", "Laptop", op); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(ctx, bob.ID, "BOB-PEER", "Phone"); err != nil {
		t.Fatalf("other user blocked: %v", err)
	}
	op, err = db.BeginOperation(ctx, alice.ID, "add-folder", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.BlockUser(ctx, alice.ID, "unowned config"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "new", "Data", op); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("drift lock bypassed: %v", err)
	}
	if err := db.CompleteOperation(ctx, alice.ID, op); !errors.Is(err, store.ErrBlocked) {
		t.Fatalf("drift operation cleared: %v", err)
	}
	if err := db.ResolveUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "new", "Data"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(ctx, alice.ID, "different", "Data"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate directory allowed: %v", err)
	}
	if err := db.DeleteFolder(ctx, bob.ID, "new"); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("other owner deleted: %v", err)
	}
}

func TestConcurrentDeviceClaimHasExactlyOneOwner(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	alice, bob := user(t, db, "alice"), user(t, db, "bob")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{alice.ID, bob.ID} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- db.CreateDevice(ctx, id, "SHARED-PEER", "Client") }()
	}
	wg.Wait()
	close(results)
	var success, conflicts int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("device ownership outcomes: success=%d conflict=%d", success, conflicts)
	}
	owner, err := db.DeviceOwner(ctx, "SHARED-PEER")
	if err != nil || (owner.UserID != alice.ID && owner.UserID != bob.ID) {
		t.Fatalf("ambiguous owner: %+v %v", owner, err)
	}
}

func TestSessionsAreHashedExpireAndCanBeRevoked(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	alice := user(t, db, "alice")
	if err := db.CreateSession(ctx, "unguessable-secret", alice.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	found, err := db.SessionUser(ctx, "unguessable-secret")
	if err != nil || found.ID != alice.ID {
		t.Fatalf("session lookup: %+v %v", found, err)
	}
	if _, err := db.SessionUser(ctx, "other-secret"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("forged session: %v", err)
	}
	if err := db.DeleteSession(ctx, "unguessable-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionUser(ctx, "unguessable-secret"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked session: %v", err)
	}
	if err := db.CreateSession(ctx, "expired-secret", alice.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SessionUser(ctx, "expired-secret"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
}
