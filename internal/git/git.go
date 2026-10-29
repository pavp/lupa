// Package git runs the git commands lupa needs against a local repository.
package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
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

// Read returns the content of path at the reader's revision.
func (b *BlobReader) Read(path string) ([]byte, error) {
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
