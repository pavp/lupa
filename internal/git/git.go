// Package git runs the git commands lupa needs against a local repository.
package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ErrNotRepository means the directory is not inside a git work tree.
var ErrNotRepository = errors.New("not a git repository")

// UnknownRevisionError names a revision git could not resolve to a commit.
type UnknownRevisionError struct{ Rev string }

func (e UnknownRevisionError) Error() string { return fmt.Sprintf("unknown revision %q", e.Rev) }

// FileChange is one file's line counts between two commits. Binary files have no line counts.
type FileChange struct {
	Path      string
	Additions int
	Deletions int
	Binary    bool
}

// Repo is a local repository rooted at or below Dir.
type Repo struct{ Dir string }

func (r Repo) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// Remotes maps each remote's name to its configured URL and, when url.<base>.insteadOf rewrites it,
// the rewritten URL.
func (r Repo) Remotes() (map[string][]string, error) {
	out, err := r.run("config", "--get-regexp", `^remote\..*\.url$`)
	remotes := map[string][]string{}
	if err != nil {
		// git config exits 1 when nothing matches: a clone with no remotes.
		return remotes, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		key, url, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		remotes[name] = append(remotes[name], url)
		// ls-remote applies url.<base>.insteadOf, which config leaves raw.
		if resolved, err := r.run("ls-remote", "--get-url", name); err == nil && strings.TrimSpace(resolved) != url {
			remotes[name] = append(remotes[name], strings.TrimSpace(resolved))
		}
	}
	return remotes, nil
}

// CloneBlobless clones url into dir with history and trees but no file contents, which git fetches
// when they are first read. dir must not exist yet.
func CloneBlobless(url, dir string) error {
	_, err := Repo{}.run("clone", "--quiet", "--filter=blob:none", "--no-checkout", url, dir)
	return err
}

// Blobless reports a partial clone, whose file contents arrive on first read.
func (r Repo) Blobless() bool {
	out, err := r.run("config", "--get", "remote.origin.promisor")
	return err == nil && strings.TrimSpace(out) == "true"
}

// Prefetch brings in every file content of rev in one request, by checking rev out in a throwaway
// worktree; in a blobless clone, reading them one by one would cost a request each.
func (r Repo) Prefetch(rev string) error {
	_, remove, err := r.Worktree(rev)
	if err != nil {
		return err
	}
	return remove()
}

// Fetch fetches refspecs from remote without tags; it never touches the checkout.
func (r Repo) Fetch(remote string, refspecs ...string) error {
	_, err := r.run(append([]string{"fetch", "--quiet", "--no-tags", remote}, refspecs...)...)
	return err
}

// Check fails with ErrNotRepository when Dir is not inside a git work tree.
func (r Repo) Check() error {
	if _, err := r.run("rev-parse", "--git-dir"); err != nil {
		return ErrNotRepository
	}
	return nil
}

// Commit resolves rev to a full commit hash.
func (r Repo) Commit(rev string) (string, error) {
	out, err := r.run("rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", UnknownRevisionError{Rev: rev}
	}
	return strings.TrimSpace(out), nil
}

// MergeBase returns the best common ancestor of two commits.
func (r Repo) MergeBase(a, b string) (string, error) {
	out, err := r.run("merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DefaultBranch returns the remote's default branch when known, else the first of main and master that exists.
func (r Repo) DefaultBranch() (string, error) {
	if out, err := r.run("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(out), nil
	}
	for _, name := range []string{"main", "master"} {
		if _, err := r.Commit(name); err == nil {
			return name, nil
		}
	}
	return "", errors.New("cannot find the default branch: no origin/HEAD, main or master")
}

// NumStat returns the per-file line counts of the diff from base to head, renames shown as delete plus add.
func (r Repo) NumStat(base, head string) ([]FileChange, error) {
	out, err := r.run("diff", "--numstat", "-z", "--no-renames", base, head)
	if err != nil {
		return nil, err
	}
	var changes []FileChange
	for _, rec := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, "\t", 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected numstat record %q", rec)
		}
		c := FileChange{Path: fields[2]}
		if fields[0] == "-" {
			c.Binary = true
		} else {
			if c.Additions, err = strconv.Atoi(fields[0]); err != nil {
				return nil, fmt.Errorf("numstat additions %q: %w", fields[0], err)
			}
			if c.Deletions, err = strconv.Atoi(fields[1]); err != nil {
				return nil, fmt.Errorf("numstat deletions %q: %w", fields[1], err)
			}
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// Files lists every file tracked at rev.
func (r Repo) Files(rev string) ([]string, error) {
	out, err := r.run("ls-tree", "-r", "-z", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

// BlobReader reads files at one revision through a single `git cat-file --batch` process.
type BlobReader struct {
	// mu serializes reads: one cat-file process answers them in order over one pipe.
	mu    sync.Mutex
	rev   string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
}

// Reader starts a BlobReader at rev; Close it when done.
func (r Repo) Reader(rev string) (*BlobReader, error) {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = r.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &BlobReader{rev: rev, cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout)}, nil
}

// Read returns the content of path at the reader's revision. It is safe for concurrent use.
func (b *BlobReader) Read(path string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := fmt.Fprintf(b.stdin, "%s:%s\n", b.rev, path); err != nil {
		return nil, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, fmt.Errorf("%s:%s: %s", b.rev, path, strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, fmt.Errorf("cat-file header %q: %w", header, err)
	}
	content := make([]byte, size+1)
	if _, err := io.ReadFull(b.out, content); err != nil {
		return nil, err
	}
	return content[:size], nil
}

// Close stops the cat-file process.
func (b *BlobReader) Close() error {
	b.stdin.Close()
	return b.cmd.Wait()
}

// Show returns the content of path at rev; ok is false when the file does not exist there.
func (r Repo) Show(rev, path string) (content []byte, ok bool) {
	out, err := r.run("show", rev+":"+path)
	if err != nil {
		return nil, false
	}
	return []byte(out), true
}

// GitDir returns the absolute path of the repository's git directory.
func (r Repo) GitDir() (string, error) {
	out, err := r.run("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// FirstChangedLines returns, per file, the first line the change touches on the head side.
func (r Repo) FirstChangedLines(base, head string) (map[string]int, error) {
	out, err := r.run("diff", "--unified=0", "--no-renames", "--no-color", base, head)
	if err != nil {
		return nil, err
	}
	lines := map[string]int{}
	file := ""
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if file == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(l, "@@ ") && file != "":
			if _, seen := lines[file]; seen {
				continue
			}
			// @@ -a,b +c,d @@: c is the first line on the head side.
			fields := strings.Fields(l)
			if len(fields) < 3 {
				continue
			}
			start, _, _ := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
			if n, err := strconv.Atoi(start); err == nil {
				lines[file] = max(n, 1)
			}
		}
	}
	return lines, nil
}

// Patches returns the unified diff from base to head per file, keyed by path (the old path for a deletion).
func (r Repo) Patches(base, head string) (map[string]string, error) {
	out, err := r.run("-c", "core.quotePath=false", "diff", "--no-renames", "--no-color", "--unified=3", base, head)
	if err != nil {
		return nil, err
	}
	patches := map[string]string{}
	for _, p := range strings.SplitAfter(out, "\ndiff --git ") {
		if !strings.HasPrefix(p, "diff --git ") {
			p = "diff --git " + p
		}
		p = strings.TrimSuffix(p, "diff --git ")
		// Without renames the header is `diff --git a/<path> b/<path>` with the same path twice, so its
		// length gives the path even when the path holds spaces.
		names, _, _ := strings.Cut(strings.TrimPrefix(p, "diff --git "), "\n")
		if n := (len(names) - 5) / 2; n > 0 {
			patches[names[2:2+n]] = p
		}
	}
	return patches, nil
}

// Worktree checks rev out in a detached worktree in a fresh directory outside the repository.
// remove deletes the worktree and its directory; calling it again does nothing.
func (r Repo) Worktree(rev string) (dir string, remove func() error, err error) {
	commit, err := r.Commit(rev)
	if err != nil {
		return "", nil, err
	}
	parent, err := os.MkdirTemp("", "lupa-review-*")
	if err != nil {
		return "", nil, err
	}
	dir = filepath.Join(parent, "tree")
	if _, err := r.run("worktree", "add", "--detach", "--quiet", dir, commit); err != nil {
		return "", nil, errors.Join(err, os.RemoveAll(parent))
	}
	removed := false
	remove = func() error {
		if removed {
			return nil
		}
		removed = true
		_, err := r.run("worktree", "remove", "--force", dir)
		return errors.Join(err, os.RemoveAll(parent))
	}
	return dir, remove, nil
}
