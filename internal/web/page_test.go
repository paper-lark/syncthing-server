package web_test

import (
	"context"
	"strings"
	"testing"

	"syncthing-server/internal/store"
)

func TestResourcePageHasAccessibleNavigationAndActions(t *testing.T) {
	handler, db, cookie := fixture(t)
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolderDetails(context.Background(), store.Folder{ID: "photos", UserID: user.ID, Label: "Photos", Group: "Family", Subpath: "my-photos"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(context.Background(), user.ID, "peer", "Laptop"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateDevice(context.Background(), user.ID, "spare", "Tablet"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetShare(context.Background(), user.ID, "photos", "peer", true); err != nil {
		t.Fatal(err)
	}

	page := serve(handler, cookie, "GET", "/", nil)
	if page.Code != 200 {
		t.Fatalf("dashboard: %d %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, fragment := range []string{
		`name="viewport"`, `aria-current="page"`, `id="create-folder"`, `id="add-peer"`,
		`<dialog class="creation-dialog" id="folder-dialog"`, `<dialog class="creation-dialog" id="peer-dialog"`, `src="/assets/dialogs.js"`,
		`action="/folders"`, `action="/devices"`, `action="/folders/photos/shares"`,
		`action="/folders/photos/delete"`, `action="/devices/peer/delete"`,
		`name="csrf" value="`, `name="label"`, `name="group"`, `name="id"`, `name="subpath"`,
		`name="device"`, `name="action" value="add"`, `name="action" value="remove"`,
		`name="confirm"`, `Group: Family`, `Subpath: my-photos`, `Server last scan: Unavailable`,
		`Shared with`, `name="device" value="peer"`, `Unshare Laptop from Photos`, `Sync: Unavailable`, `Last seen: Unavailable`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("dashboard missing %q", fragment)
		}
	}
	if strings.Contains(body, user.StorageArea) {
		t.Fatal("dashboard exposed the private storage area")
	}

	admin := serve(handler, cookie, "GET", "/admin", nil)
	if admin.Code != 200 {
		t.Fatalf("admin overview: %d %s", admin.Code, admin.Body.String())
	}
	body = admin.Body.String()
	for _, fragment := range []string{`Photos`, `Laptop`, `Shared with`, `Server last scan: Unavailable`, `Group: Family`, `Subpath: my-photos`, `action="/logout"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("overview missing %q", fragment)
		}
	}
	for _, fragment := range []string{`action="/folders`, `action="/devices`, `id="create-folder"`, `id="add-peer"`, `src="/assets/dialogs.js"`, `<dialog`, `Server device ID`, user.StorageArea} {
		if strings.Contains(body, fragment) {
			t.Errorf("overview leaked write controls or private data: %q", fragment)
		}
	}
}

func TestDialogScriptIsSameOriginAndFormsHaveFallback(t *testing.T) {
	handler, _, cookie := fixture(t)
	page := serve(handler, cookie, "GET", "/", nil)
	if page.Code != 200 || !strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'self'") || strings.Contains(page.Header().Get("Content-Security-Policy"), "script-src 'unsafe-inline'") {
		t.Fatalf("page does not allow only the local dialog script: %d %q", page.Code, page.Header().Get("Content-Security-Policy"))
	}
	for _, fragment := range []string{`<details class="create-fallback" id="create-folder">`, `<details class="create-fallback" id="add-peer">`, `aria-labelledby="folder-dialog-heading"`, `aria-labelledby="peer-dialog-heading"`} {
		if !strings.Contains(page.Body.String(), fragment) {
			t.Errorf("missing dialog or no-script fallback %q", fragment)
		}
	}
	asset := serve(handler, nil, "GET", "/assets/dialogs.js", nil)
	if asset.Code != 200 || !strings.HasPrefix(asset.Header().Get("Content-Type"), "application/javascript") || !strings.Contains(asset.Body.String(), "showModal") {
		t.Fatalf("dialog script unavailable: %d %q", asset.Code, asset.Header().Get("Content-Type"))
	}
}

func TestFolderWithoutPeersOffersPairingInsteadOfShareForm(t *testing.T) {
	handler, db, cookie := fixture(t)
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolder(context.Background(), user.ID, "photos", "Photos"); err != nil {
		t.Fatal(err)
	}
	page := serve(handler, cookie, "GET", "/", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Add a peer to share this folder.") || strings.Contains(page.Body.String(), `action="/folders/photos/shares"`) {
		t.Fatalf("folder with no peers rendered inactive sharing controls: %d %s", page.Code, page.Body.String())
	}
}

func TestResourcePageEscapesUserContent(t *testing.T) {
	handler, db, cookie := fixture(t)
	user, err := db.FindUser(context.Background(), "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFolderDetails(context.Background(), store.Folder{ID: "safe-id", UserID: user.ID, Label: `<script>alert(1)</script>`, Subpath: "safe-path"}); err != nil {
		t.Fatal(err)
	}
	page := serve(handler, cookie, "GET", "/", nil)
	if page.Code != 200 || strings.Contains(page.Body.String(), `<script>`) || !strings.Contains(page.Body.String(), `&lt;script&gt;`) {
		t.Fatalf("HTML escaping failed: %d %s", page.Code, page.Body.String())
	}
}
