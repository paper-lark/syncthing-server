package syncthing_test

import (
	"context"
	"os"
	"testing"
	"time"

	"syncthing-server/internal/syncthing"
)

func TestLiveIgnoredFolderOffer(t *testing.T) {
	base, key := os.Getenv("ST_SMOKE_URL"), os.Getenv("ST_SMOKE_API_KEY")
	if base == "" || key == "" || os.Getenv("ST_SMOKE_DATA_ROOT") == "" {
		t.Skip("requires disposable daemon smoke settings")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := syncthing.NewClient(base, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	const peer = "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	if err := client.CreateDevice(ctx, syncthing.Device{DeviceID: peer, Name: "Disposable peer"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := client.DeleteDevice(cleanupCtx, peer); err != nil {
			t.Errorf("cleanup peer: %v", err)
		}
	})
	if _, err := client.PendingDevices(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PendingFolders(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.IgnorePendingFolder(ctx, "unrequested-folder", peer, "Unrequested"); err != nil {
		t.Fatal(err)
	}
	device, err := client.Device(ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ignored := range device.IgnoredFolders {
		if ignored.ID == "unrequested-folder" {
			found = true
		}
	}
	if !found {
		t.Fatal("daemon did not persist ignored folder offer")
	}
}
