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

// ours fails when path holds a skill this command did not write.
func ours(path string) error {
	got, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !bytes.Contains(got, marker):
		return fmt.Errorf("%s holds a lupa skill lupa did not write; move it away to install", path)
	}
	return nil
}

// linkable fails when link exists and is anything but a link to dir or a skill this command wrote.
func linkable(link, dir string) error {
	info, err := os.Lstat(link)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		if dest, _ := os.Readlink(link); dest == dir {
			return nil
		}
		return fmt.Errorf("%s links elsewhere; remove it to install", link)
	}
	if err := ours(filepath.Join(link, "SKILL.md")); err != nil {
		return fmt.Errorf("%s holds a lupa skill lupa did not write; move it away to install", link)
	}
	return nil
}
