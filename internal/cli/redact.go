package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pavp/lupa/internal/credentials"
)

// redactTree replaces credentials in the text files of lupa's own review worktree, so an agent that opens
// a file reads them redacted too, and removes the links that lead out of it, which would give the agent a
// path to files nobody redacted. Git metadata and binary files are left alone.
func redactTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.Name() == ".git":
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.Type()&fs.ModeSymlink != 0:
			if leavesTree(root, p) {
				return os.Remove(p)
			}
			return nil
		case !d.Type().IsRegular():
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return err
		}
		if r := credentials.Redact(string(b)); r != string(b) {
			return os.WriteFile(p, []byte(r), 0o644)
		}
		return nil
	})
}

// leavesTree reports a link whose target, read lexically, is outside root; a link to another link inside
// the tree is judged when the walk reaches that one.
func leavesTree(root, link string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return true
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	rel, err := filepath.Rel(root, filepath.Clean(target))
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
