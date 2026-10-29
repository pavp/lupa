package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func worktreeRepo(t *testing.T) (repo, rev string) {
	t.Helper()
	repo = t.TempDir()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "-q", "-m", "one")
	rev = gitCmd(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repo, "commit", "-q", "-am", "two")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, rev
}

func TestWorktreeChecksOutRevDetachedOutsideTheRepo(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	repo, rev := worktreeRepo(t)
	head := gitCmd(t, repo, "rev-parse", "HEAD")

	dir, remove, err := Repo{Dir: repo}.Worktree(rev)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remove() })

	realRepo, _ := filepath.EvalSymlinks(repo)
	realDir, _ := filepath.EvalSymlinks(dir)
	if rel, err := filepath.Rel(realRepo, realDir); err != nil || !strings.HasPrefix(rel, "..") {
		t.Errorf("worktree %s is inside the repo %s", realDir, realRepo)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(got) != "v1\n" {
		t.Errorf("a.txt in worktree = %q, %v; want the content at rev", got, err)
	}
	if got := gitCmd(t, dir, "rev-parse", "HEAD"); got != rev {
		t.Errorf("worktree HEAD = %s, want %s", got, rev)
	}
	if out, err := exec.Command("git", "-C", dir, "symbolic-ref", "-q", "HEAD").Output(); err == nil {
		t.Errorf("worktree HEAD is on a branch %q, want detached", out)
	}

	if got := gitCmd(t, repo, "branch", "--show-current"); got != "main" {
		t.Errorf("main checkout branch = %q, want main", got)
	}
	if got := gitCmd(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("main checkout HEAD = %s, want %s", got, head)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "uncommitted\n" {
		t.Errorf("uncommitted change lost: a.txt = %q", got)
	}
	if got := gitCmd(t, repo, "status", "--porcelain"); got != "M a.txt" {
		t.Errorf("main checkout status = %q, want only the unstaged edit", got)
	}

	if err := remove(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after remove: %v", err)
	}
	if list := gitCmd(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktree still registered after remove:\n%s", list)
	}
	if err := remove(); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if left, _ := os.ReadDir(os.TempDir()); len(left) != 0 {
		t.Errorf("temp dir not cleaned: %v", left)
	}
}

func TestWorktreeUnknownRevLeavesNothingBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	repo, _ := worktreeRepo(t)

	dir, remove, err := Repo{Dir: repo}.Worktree("no-such-rev")
	if err == nil {
		_ = remove()
		t.Fatalf("Worktree(no-such-rev) = %s, want an error", dir)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("directories left behind: %v", left)
	}
	if list := gitCmd(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktree registered after a failed add:\n%s", list)
	}
}

func TestBlobReaderIsSafeForConcurrentReads(t *testing.T) {
	repo := t.TempDir()
	gitCmd(t, repo, "init", "-q", "-b", "main")
	want := map[string]string{}
	for i := range 8 {
		name := fmt.Sprintf("f%d.txt", i)
		want[name] = strings.Repeat(fmt.Sprintf("line %d\n", i), 4000)
		if err := os.WriteFile(filepath.Join(repo, name), []byte(want[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd(t, repo, "add", ".")
	gitCmd(t, repo, "commit", "-q", "-m", "files")
	r, err := Repo{Dir: repo}.Reader("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var wg sync.WaitGroup
	for name, content := range want {
		for range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := r.Read(name)
				if err != nil || string(got) != content {
					t.Errorf("Read(%s) = %d bytes, err %v; want its %d bytes", name, len(got), err, len(content))
				}
			}()
		}
	}
	wg.Wait()
}

func TestRemotesApplyURLRewrites(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "remote", "add", "origin", "gh:pavp/lupa")
	gitCmd(t, dir, "remote", "add", "plain", "git@github.com:acme/shop.git")
	gitCmd(t, dir, "config", "url.https://github.com/.insteadOf", "gh:")
	got, err := Repo{Dir: dir}.Remotes()
	want := map[string][]string{"origin": {"gh:pavp/lupa", "https://github.com/pavp/lupa"}, "plain": {"git@github.com:acme/shop.git"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("remotes = %v (%v), want %v: the raw URL, then the rewritten one when it differs", got, err, want)
	}
}
