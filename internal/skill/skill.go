// Package skill holds the agent skill that wraps lupa, shipped inside the binary so it always matches it.
package skill

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var content []byte

// marker is the frontmatter line that tells a skill this command wrote from one the user wrote.
var marker = []byte("generated-by: lupa skill install")

// Install writes the skill to home/.agents/skills/lupa and links home/.claude/skills/lupa to it, replacing only
// what an earlier install wrote; it returns the paths it wrote. A lupa skill someone else wrote is left alone.
func Install(home string) ([]string, error) {
	dir := filepath.Join(home, ".agents", "skills", "lupa")
	file := filepath.Join(dir, "SKILL.md")
	if err := ours(file); err != nil {
		return nil, err
	}
	link := filepath.Join(home, ".claude", "skills", "lupa")
	if err := linkable(link, dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, content, 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return nil, err
	}
	// An earlier copy written as a directory (checked to be ours above) gives way to the link.
	if info, err := os.Lstat(link); err == nil && info.Mode()&fs.ModeSymlink == 0 {
		if err := os.RemoveAll(link); err != nil {
			return nil, err
		}
	}
	if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
		if err := os.Symlink(dir, link); err != nil {
			return nil, err
		}
	}
	return []string{file, link}, nil
}

// ours fails when path holds a skill this command did not write; a missing file is fine, nothing is there.
func ours(path string) error {
	got, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s: %w", path, err)
	case !marked(got):
		return fmt.Errorf("%s holds a lupa skill lupa did not write; move it away to install", path)
	}
	return nil
}

// marked reports the marker inside the frontmatter, so a skill that only mentions it in its body is not taken for ours.
func marked(skill []byte) bool {
	front, _, ok := bytes.Cut(bytes.TrimPrefix(skill, []byte("---\n")), []byte("\n---\n"))
	return ok && bytes.HasPrefix(skill, []byte("---\n")) && bytes.Contains(front, marker)
}

// linkable fails when link exists and is anything but a link to dir, or a directory an earlier install wrote
// that holds only its skill file: a directory without that file, or with anything added, is the user's.
func linkable(link, dir string) error {
	info, err := os.Lstat(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		dest, _ := os.Readlink(link)
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(link), dest)
		}
		if filepath.Clean(dest) == filepath.Clean(dir) {
			return nil
		}
		return fmt.Errorf("%s links elsewhere; remove it to install", link)
	case !info.IsDir():
		return fmt.Errorf("%s is a file; move it away to install", link)
	}
	entries, err := os.ReadDir(link)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != "SKILL.md" {
		return fmt.Errorf("%s holds files lupa did not write; move it away to install", link)
	}
	got, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || !marked(got) {
		return fmt.Errorf("%s holds a lupa skill lupa did not write; move it away to install", link)
	}
	return nil
}
