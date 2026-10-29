package cli

import (
	"bytes"
	"encoding/json"
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

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo returns a repo whose `feature` branch changes a.txt (+2 −1) and adds b.go (+3) on top of main.
func newRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	base = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "a.txt", "one\n2\nthree\n")
	write(t, dir, "b.go", "package b\n\nvar B = 1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "change")
	head = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "main")
	return dir, base, head
}

func lupa(dir string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, Env{Dir: dir, Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

type signals struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Files  []struct {
		Path      string `json:"path"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	} `json:"files"`
	Size struct {
		Files     int `json:"files"`
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Changed   int `json:"changed"`
	} `json:"size"`
}

func TestMapJSON(t *testing.T) {
	dir, base, head := newRepo(t)
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "main moves on")
	for _, target := range []string{"main..feature", "feature"} {
		t.Run(target, func(t *testing.T) {
			code, stdout, stderr := lupa(dir, "map", target, "--json")
			if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			var got signals
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
			}
			if got.Schema != "lupa.signals/v0" || got.Base != base || got.Head != head {
				t.Errorf("schema/base/head = %q %q %q, want lupa.signals/v0 %q (merge-base) %q", got.Schema, got.Base, got.Head, base, head)
			}
			if len(got.Files) != 2 ||
				got.Files[0].Path != "a.txt" || got.Files[0].Additions != 2 || got.Files[0].Deletions != 1 ||
				got.Files[1].Path != "b.go" || got.Files[1].Additions != 3 || got.Files[1].Deletions != 0 {
				t.Errorf("files = %+v", got.Files)
			}
			if got.Size.Files != 2 || got.Size.Additions != 5 || got.Size.Deletions != 1 || got.Size.Changed != 6 {
				t.Errorf("size = %+v", got.Size)
			}
		})
	}
}

func TestMapBinaryFile(t *testing.T) {
	dir, _, _ := newRepo(t)
	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "logo.png", "\x89PNG\x00\x01\x02")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "logo")
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"path": "logo.png",`) || !strings.Contains(stdout, `"binary": true`) {
		t.Errorf("binary file not reported as binary:\n%s", stdout)
	}
}

func TestMapText(t *testing.T) {
	dir, _, _ := newRepo(t)
	code, stdout, stderr := lupa(dir, "map", "feature")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if want := "2 files, +5 -1 (6 changed lines)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestErrors(t *testing.T) {
	dir, _, _ := newRepo(t)
	cases := []struct {
		name       string
		dir        string
		args       []string
		wantStderr string
	}{
		{"no command", dir, nil, "usage: lupa map"},
		{"unknown command", dir, []string{"nope"}, "unknown command \"nope\""},
		{"map without target", dir, []string{"map"}, "usage: lupa map"},
		{"unknown flag", dir, []string{"map", "feature", "--yaml"}, "unknown flag \"--yaml\""},
		{"unknown ref", dir, []string{"map", "main..missing"}, "unknown revision \"missing\""},
		{"not a git repo", t.TempDir(), []string{"map", "feature"}, "not a git repository"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := lupa(c.dir, c.args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, c.wantStderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2, no stdout, stderr containing %q", code, stdout, stderr, c.wantStderr)
			}
		})
	}
}
