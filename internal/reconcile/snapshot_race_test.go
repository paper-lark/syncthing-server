package reconcile_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"syncthing-server/internal/reconcile"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

func TestReconcileSnapshotCannotRaceWithOwnedDeviceDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	owner, err := db.UpsertUser(ctx, "issuer", "owner", "Owner", "", true)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.UpsertUser(ctx, "issuer", "other", "Other", "", true)
	if err != nil {
		t.Fatal(err)
	}
	id := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	if err := db.CreateDevice(ctx, owner.ID, id, "Peer"); err != nil {
		t.Fatal(err)
	}
	statusReached := make(chan struct{})
	releaseStatus := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	present := true
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/config/folders":
			io.WriteString(w, `[]`)
		case "/rest/config/devices":
			mu.Lock()
			exists := present
			mu.Unlock()
			if exists {
				json.NewEncoder(w).Encode([]syncthing.Device{{DeviceID: "SERVER"}, {DeviceID: id, Name: "Peer"}})
			} else {
				io.WriteString(w, `[{"deviceID":"SERVER"}]`)
			}
		case "/rest/system/status":
			once.Do(func() { close(statusReached); <-releaseStatus })
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
	scanDone := make(chan error, 1)
	go func() { scanDone <- checker.Check(ctx) }()
	select {
	case <-statusReached:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach delayed status")
	}
	deletionDone := make(chan error, 1)
	go func() {
		unlock := db.LockChanges()
		defer unlock()
		if err := db.DeleteDevice(ctx, owner.ID, id); err != nil {
			deletionDone <- err
			return
		}
		mu.Lock()
		present = false
		mu.Unlock()
		deletionDone <- nil
	}()
	select {
	case err := <-deletionDone:
		t.Fatalf("deletion overtook snapshot: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(releaseStatus)
	if err := <-scanDone; err != nil {
		t.Fatalf("legitimate snapshot falsely marked orphan: %v", err)
	}
	if err := <-deletionDone; err != nil {
		t.Fatal(err)
	}
	if err := checker.Check(ctx); err != nil {
		t.Fatalf("completed deletion falsely marked orphan: %v", err)
	}
	blocked, _, err := db.WriteStatus(ctx, other.ID)
	if err != nil || blocked {
		t.Fatalf("unrelated user blocked: %v %v", blocked, err)
	}
}
