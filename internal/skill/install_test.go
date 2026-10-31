package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallLeavesWhatItDidNotWrite(t *testing.T) {
	ours := string(content)
	cases := []struct {
		name  string
		setup func(home string)
		keep  string
	}{
		{"a directory without a skill file", func(home string) {
			put(t, filepath.Join(home, ".claude/skills/lupa/notes.md"), "mine\n")
		}, ".claude/skills/lupa/notes.md"},
		{"an earlier copy the user added files to", func(home string) {
			put(t, filepath.Join(home, ".claude/skills/lupa/SKILL.md"), ours)
			put(t, filepath.Join(home, ".claude/skills/lupa/my-notes.md"), "mine\n")
		}, ".claude/skills/lupa/my-notes.md"},
		{"a skill that only quotes the marker in its body", func(home string) {
			put(t, filepath.Join(home, ".agents/skills/lupa/SKILL.md"), "---\nname: lupa\n---\nNot ours, mentions generated-by: lupa skill install.\n")
		}, ".agents/skills/lupa/SKILL.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			c.setup(home)
			before, _ := os.ReadFile(filepath.Join(home, c.keep))
			_, err := Install(home)
			after, readErr := os.ReadFile(filepath.Join(home, c.keep))
			if err == nil || readErr != nil || string(after) != string(before) {
				t.Errorf("Install err = %v; %s now %q (%v), want an error and the file untouched", err, c.keep, after, readErr)
			}
		})
	}
}

func TestInstallAcceptsARelativeLinkToItsSkill(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/lupa", filepath.Join(home, ".claude/skills/lupa")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(home); err != nil {
		t.Errorf("Install with a relative link to its own skill = %v, want nil", err)
	}
	if got, err := os.ReadFile(filepath.Join(home, ".claude/skills/lupa/SKILL.md")); err != nil || !strings.HasPrefix(string(got), "---\nname: lupa\n") {
		t.Errorf("skill through the relative link = %q, %v", got, err)
	}
}
