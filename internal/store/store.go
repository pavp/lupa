// Package store keeps lupa's output under the reviewed repo's git dir, never in its working tree.
package store

import (
	"os"
	"path/filepath"
	"strings"
)

// Store writes maps under Root, one directory per target and head: <Root>/<key>/<short-sha>/.
type Store struct{ Root string }

const lastFile = "last"

// Save writes the signals and the report of key at head, and records head as the last map of key.
// A nil report is not written.
func (s Store) Save(key, head string, signals, report []byte) (string, error) {
	dir := filepath.Join(s.Root, slug(key), short(head))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "signals.json"), signals, 0o644); err != nil {
		return "", err
	}
	if report != nil {
		if err := os.WriteFile(filepath.Join(dir, "map.md"), report, 0o644); err != nil {
			return "", err
		}
	}
	return dir, os.WriteFile(filepath.Join(s.Root, slug(key), lastFile), []byte(head+"\n"), 0o644)
}

// Put writes data as the file name beside the map of key at head, and returns its path.
func (s Store) Put(key, head, name string, data []byte) (string, error) {
	dir := filepath.Join(s.Root, slug(key), short(head))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, data, 0o644)
}

// Last returns the head of the last saved map of key.
func (s Store) Last(key string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(s.Root, slug(key), lastFile))
	if err != nil {
		return "", false
	}
	head := strings.TrimSpace(string(b))
	return head, head != ""
}

// slug turns a target such as `feature/x` or `main..feature` into one directory name.
func slug(key string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(key)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
