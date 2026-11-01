package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRedactTreeRemovesLinksThatLeaveTheTree(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.env")
	write(t, filepath.Dir(outside), "outside.env", "TOKEN=ghp_A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1\n")
	root := t.TempDir()
	write(t, root, "inside.txt", "plain\n")
	if err := os.Symlink(outside, filepath.Join(root, "link.env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "local")); err != nil {
		t.Fatal(err)
	}
	if err := redactTree(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link.env")); !os.IsNotExist(err) {
		t.Errorf("a link out of the tree is still there: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "local")); err != nil {
		t.Errorf("a link inside the tree was removed: %v", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "TOKEN=ghp_A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1A1\n" {
		t.Errorf("the file outside the tree changed: %q", b)
	}
}
