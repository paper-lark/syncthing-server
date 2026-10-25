package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"syncthing-server/internal/storage"
)

func TestCreateAndRemoveConfinedFolder(t *testing.T) {
	root := t.TempDir()
	fs, err := storage.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	p, err := fs.Create("owner-1", "Family photos")
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "owner-1", "Family photos") {
		t.Fatalf("unexpected internal path: %q", p)
	}
	if err := os.WriteFile(filepath.Join(p, "picture.jpg"), []byte("family"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("owner-1", "Family photos"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("folder remains: %v", err)
	}
}

func TestRejectUnsafeNamesAndExistingDirectories(t *testing.T) {
	fs, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	for _, name := range []string{"..", "../sibling", "a/b", "a\\b", "", ".", " name ", "a\nline"} {
		if _, err := fs.Create("owner-1", name); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	if _, err := fs.Create("owner-1", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Create("owner-1", "existing"); err == nil {
		t.Fatal("adopted existing directory")
	}
}

func TestRejectSymlinkedAreaAndFolder(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	fs, err := storage.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if _, err := fs.Create("other", "taken"); err == nil {
		t.Fatal("followed symlink to another area")
	}
	if _, err := fs.Create("owner-1", "new"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "owner-1", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("owner-1", "linked"); err == nil {
		t.Fatal("accepted symlink for deletion")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
}
