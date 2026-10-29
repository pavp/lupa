// Package git runs the git commands lupa needs against a local repository.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotRepository means the directory is not inside a git work tree.
var ErrNotRepository = errors.New("not a git repository")

// UnknownRevisionError names a revision git could not resolve to a commit.
type UnknownRevisionError struct{ Rev string }

func (e UnknownRevisionError) Error() string { return fmt.Sprintf("unknown revision %q", e.Rev) }

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
