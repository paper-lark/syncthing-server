package syncthing_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"syncthing-server/internal/syncthing"
)

// TestLiveFolderRoundTrip is opt-in: it changes a disposable daemon's config.
// The daemon's data root must be ephemeral, and the API must not be public.
func TestLiveFolderRoundTrip(t *testing.T) {
	base, key, root := os.Getenv("ST_SMOKE_URL"), os.Getenv("ST_SMOKE_API_KEY"), os.Getenv("ST_SMOKE_DATA_ROOT")
	if base == "" || key == "" || root == "" {
		t.Skip("set ST_SMOKE_URL, ST_SMOKE_API_KEY, and ST_SMOKE_DATA_ROOT for a disposable daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client, err := syncthing.NewClient(base, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Version(ctx)
	if err != nil || !strings.HasPrefix(version.Version, "v2.") {
		t.Fatalf("unexpected daemon version %+v, err=%v", version, err)
	}
	serverID, err := client.MyID(ctx)
	if err != nil || serverID == "" {
		t.Fatalf("missing server device ID: %v", err)
	}
	id := fmt.Sprintf("wrapper-smoke-%d", time.Now().UnixNano())
	folder := syncthing.Folder{ID: id, Label: "Wrapper smoke", Path: root + "/" + id, Type: "sendreceive", Devices: []syncthing.FolderDevice{{DeviceID: serverID}}}
	if err := client.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := client.DeleteFolder(cleanupCtx, id); err != nil {
			t.Errorf("remove test folder: %v", err)
		}
	})
	got, err := client.Folder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Path != folder.Path || len(got.Devices) != 1 || got.Devices[0].DeviceID != serverID {
		t.Fatalf("incorrect live folder: %+v", got)
	}
	if _, err := client.FolderStatus(ctx, id); err != nil {
		t.Fatalf("folder status: %v", err)
	}
}
