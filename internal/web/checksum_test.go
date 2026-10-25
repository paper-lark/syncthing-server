package web_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"syncthing-server/internal/store"
)

func TestMistypedDeviceChecksumDoesNotBlockUser(t *testing.T) {
	handler, db, cookie := fixture(t)
	id := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ3"
	response := serve(handler, cookie, http.MethodPost, "/devices", url.Values{"csrf": {csrf(t, handler, cookie)}, "id": {id}, "name": {"Mistyped peer"}})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("mistyped ID accepted: %d", response.Code)
	}
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := db.WriteStatus(context.Background(), user.ID)
	if err != nil || blocked {
		t.Fatalf("invalid input blocked account: %v %v", blocked, err)
	}
	if _, err := db.DeviceOwner(context.Background(), id); err != store.ErrNotFound {
		t.Fatalf("invalid device persisted: %v", err)
	}
}
