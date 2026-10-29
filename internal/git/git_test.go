package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPatchesSplitPerFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write("a.txt", "one\ntwo\n")
	write("gone.txt", "bye\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	base := gitCmd(t, dir, "rev-parse", "HEAD")
	write("a.txt", "one\n2\n")
	write("src/new file.go", "package src\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "change")
	head := gitCmd(t, dir, "rev-parse", "HEAD")

	patches, err := Repo{Dir: dir}.Patches(base, head)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"a.txt":           {"diff --git a/a.txt b/a.txt\n", "@@ -1,2 +1,2 @@\n", "-two\n+2\n"},
		"src/new file.go": {"diff --git a/src/new file.go b/src/new file.go\n", "+package src\n"},
		"gone.txt":        {"diff --git a/gone.txt b/gone.txt\n", "-bye\n"},
	}
	if len(patches) != len(want) {
		t.Fatalf("patches for %d files, want %d: %v", len(patches), len(want), patches)
	}
	for path, parts := range want {
		p := patches[path]
		if !strings.HasPrefix(p, parts[0]) {
			t.Errorf("%s patch does not start with its own header:\n%s", path, p)
		}
		for _, s := range parts[1:] {
			if !strings.Contains(p, s) {
				t.Errorf("%s patch lacks %q:\n%s", path, s, p)
			}
		}
		if strings.Count(p, "diff --git ") != 1 {
			t.Errorf("%s patch holds more than one file:\n%s", path, p)
		}
	}
}
