package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/forge/github"
)

// fakeForge answers PR with a fixed pull request and records the refs it was asked for.
type fakeForge struct {
	pr   github.PR
	err  error
	refs []github.Ref
}

func (f *fakeForge) PR(_ string, ref github.Ref) (github.PR, error) {
	f.refs = append(f.refs, ref)
	return f.pr, f.err
}

// newPRClone returns a clone whose origin reads as https://github.com/acme/shop.git but fetches
// from a local bare upstream holding PR 7 (the feature branch of newRepo) at refs/pull/7/head.
// Upstream main moved on after the feature branched, so the PR's base is the merge-base, not main.
func newPRClone(t *testing.T) (clone, base, head string) {
	t.Helper()
	src, base, head := newRepo(t)
	write(t, src, "c.txt", "later on main\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "main moves on")
	upstream := filepath.Join(t.TempDir(), "upstream.git")
	gitCmd(t, src, "clone", "-q", "--bare", src, upstream)
	gitCmd(t, src, "push", "-q", upstream, "feature:refs/pull/7/head")
	gitCmd(t, upstream, "branch", "-q", "-D", "feature")
	clone = filepath.Join(t.TempDir(), "clone")
	gitCmd(t, src, "clone", "-q", upstream, clone)
	gitCmd(t, clone, "remote", "set-url", "origin", "https://github.com/acme/shop.git")
	gitCmd(t, clone, "config", "url."+upstream+".insteadOf", "https://github.com/acme/shop.git")
	return clone, base, head
}

func prForge(head string) *fakeForge {
	return &fakeForge{pr: github.PR{Ref: github.Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 7},
		Title: "Change a and add b", Body: "Adds B.", BaseRef: "main", HeadSHA: head}}
}

func lupaWith(dir string, forge Forge, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, Env{Dir: dir, Stdout: &out, Stderr: &errOut, Forge: forge})
	return code, out.String(), errOut.String()
}

func TestMapPRFromItsClone(t *testing.T) {
	for _, target := range []string{"7", "#7", "https://github.com/acme/shop/pull/7"} {
		t.Run(target, func(t *testing.T) {
			clone, base, head := newPRClone(t)
			before := gitCmd(t, clone, "status", "--porcelain", "--branch")
			forge := prForge(head)

			code, stdout, stderr := lupaWith(clone, forge, "map", target, "--json")
			if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			var got signals
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v", err)
			}
			if got.Base != base || got.Head != head {
				t.Errorf("range = %s..%s, want the merge-base %s..%s", got.Base[:7], got.Head[:7], base[:7], head[:7])
			}
			if len(forge.refs) != 1 || forge.refs[0].Number != 7 {
				t.Errorf("forge asked for %+v, want PR 7 once", forge.refs)
			}
			if after := gitCmd(t, clone, "status", "--porcelain", "--branch"); after != before {
				t.Errorf("the checkout changed:\nbefore %q\nafter  %q", before, after)
			}
			if got := gitCmd(t, clone, "rev-parse", "refs/lupa/pr/7/head"); got != head {
				t.Errorf("refs/lupa/pr/7/head = %s, want the PR head", got)
			}
			if _, err := os.Stat(filepath.Join(clone, ".git", "lupa", "pr-7")); err != nil {
				t.Errorf("map not saved under the PR's key: %v", err)
			}
		})
	}
}

func TestReviewPlanOfAPR(t *testing.T) {
	clone, _, head := newPRClone(t)
	code, stdout, stderr := lupaWith(clone, prForge(head), "review", "7", "--plan")
	if code != 0 || !strings.Contains(stdout, "b.go") || !strings.Contains(stdout, "lupa map 7") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a plan of PR 7", code, stdout, stderr)
	}
}

func TestPRTargetErrors(t *testing.T) {
	clone, _, head := newPRClone(t)
	other := prForge(head)
	other.pr.Ref.Repo = "other"
	failing := &fakeForge{err: errors.New("gh pr view: no pull requests found for 9")}
	tests := []struct {
		name   string
		forge  Forge
		target string
		stderr string
	}{
		{"a PR of a repo this clone does not track", other, "https://github.com/acme/other/pull/7", "acme/other"},
		{"the forge fails", failing, "9", "no pull requests found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := lupaWith(clone, tt.forge, "map", tt.target)
			if code != 2 || stdout != "" || !strings.Contains(stderr, tt.stderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 2 and %q", code, stdout, stderr, tt.stderr)
			}
		})
	}
}
