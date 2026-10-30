package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/forge/github"
)

// fakeForge answers PR with a fixed pull request, OpenPRs with a fixed list, and records the refs it
// was asked for.
type fakeForge struct {
	pr      github.PR
	err     error
	open    []github.PR
	openErr error
	refs    []github.Ref
}

func (f *fakeForge) OpenPRs(github.Ref) ([]github.PR, error) { return f.open, f.openErr }

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
		Title: "Change a and add b", Body: "Adds B so that callers can read the new value without parsing a.txt.", BaseRef: "main", HeadSHA: head}}
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
		{"a PR number whose repo no remote of this clone points at", other, "7", "acme/other"},
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

// useUpstream points every git process of the test at upstream for https://github.com/acme/shop.git.
func useUpstream(t *testing.T, clone string) {
	t.Helper()
	upstream := gitCmd(t, clone, "config", "--get-regexp", `^url\..*\.insteadof$`)
	upstream = strings.TrimSuffix(strings.TrimPrefix(strings.Fields(upstream)[0], "url."), ".insteadof")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+upstream+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/shop.git")
}

func TestMapPRByURLWithoutItsClone(t *testing.T) {
	clone, base, head := newPRClone(t)
	useUpstream(t, clone)
	elsewhere, _, _ := newRepo(t)
	for name, dir := range map[string]string{"outside any repo": t.TempDir(), "inside another repo": elsewhere} {
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			run := func() (int, string, string) {
				var out, errOut bytes.Buffer
				code := Run([]string{"map", "https://github.com/acme/shop/pull/7", "--json"},
					Env{Dir: dir, Stdout: &out, Stderr: &errOut, Forge: prForge(head), CacheDir: cache})
				return code, out.String(), errOut.String()
			}
			code, stdout, stderr := run()
			if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			var got signals
			if err := json.Unmarshal([]byte(stdout), &got); err != nil || got.Base != base || got.Head != head {
				t.Fatalf("range = %+v (%v), want %s..%s", got, err, base[:7], head[:7])
			}
			cached := filepath.Join(cache, "github.com", "acme", "shop")
			if !strings.Contains(stderr, "Cloning acme/shop") {
				t.Errorf("stderr %q does not say the first run clones", stderr)
			}
			if f := gitCmd(t, cached, "config", "remote.origin.partialclonefilter"); f != "blob:none" {
				t.Errorf("cache clone filter = %q, want blob:none", f)
			}
			if _, err := os.Stat(filepath.Join(cached, ".git", "lupa", "pr-7")); err != nil {
				t.Errorf("map not saved in the cache clone: %v", err)
			}
			if code, _, stderr := run(); code != 0 || strings.Contains(stderr, "Cloning") {
				t.Errorf("second run: exit %d, stderr %q; want the cached clone reused", code, stderr)
			}
		})
	}
}

func TestPRNumberOutsideAClone(t *testing.T) {
	code, stdout, stderr := lupaWith(t.TempDir(), prForge("abc"), "map", "7")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "URL") {
		t.Errorf("exit %d, stdout %q, stderr %q; want 2 and a hint to pass the URL", code, stdout, stderr)
	}
}

func TestMapPRGate(t *testing.T) {
	clone, _, head := newPRClone(t)
	dup := prForge(head)
	dup.open = []github.PR{{Ref: github.Ref{Number: 9}, Title: "Same change", Files: []string{"a.txt", "b.go"}}}
	unlisted := prForge(head)
	unlisted.openErr = errors.New("gh pr list: rate limited")
	silent := prForge(head)
	silent.pr.Body = ""
	tests := []struct {
		name, decision, reason, stderr string
		forge                          *fakeForge
	}{
		{"possible duplicate", "hand-back", "PR #9 (Same change) changes 2 of the same 2 files", "", dup},
		{"open PRs not listed", "review", "within 400", "duplicate check skipped: gh pr list: rate limited", unlisted},
		{"no description", "hand-back", "no description", "", silent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := lupaWith(clone, tt.forge, "map", "7", "--json")
			if code != 0 || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d, stderr %q; want 0 and %q", code, stderr, tt.stderr)
			}
			var got struct {
				PR struct {
					Number int
					URL    string
				} `json:"pr"`
				ReviewNow struct{ Decision, Reason, Reply string } `json:"review_now"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatal(err)
			}
			if got.PR.Number != 7 || got.PR.URL != "https://github.com/acme/shop/pull/7" {
				t.Errorf("pr = %+v, want PR 7 with its URL", got.PR)
			}
			if got.ReviewNow.Decision != tt.decision || !strings.Contains(got.ReviewNow.Reason, tt.reason) {
				t.Errorf("review_now = %+v, want %s with %q", got.ReviewNow, tt.decision, tt.reason)
			}
		})
	}
}

func TestMapPRDescriptionVsDiff(t *testing.T) {
	clone, _, head := newPRClone(t)
	forge := prForge(head)
	forge.pr.Body = "Adds `B` in b.go and drops `retry`. Callers read the value faster."
	code, stdout, stderr := lupaWith(clone, forge, "map", "7", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Claims []struct {
			Text  string
			Cites []struct {
				Text, Path, Side, Code string
				Found                  bool
				Line                   int
			}
		} `json:"claims"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Claims) != 2 || len(got.Claims[0].Cites) != 3 || got.Claims[1].Cites != nil {
		t.Fatalf("claims = %+v, want 2 claims, the first citing 3 things and the second none", got.Claims)
	}
	b, file, retry := got.Claims[0].Cites[0], got.Claims[0].Cites[1], got.Claims[0].Cites[2]
	if !b.Found || b.Path != "b.go" || b.Side != "+" || b.Line != 3 || b.Code != "var B = 1" {
		t.Errorf("`B` = %+v, want b.go:3 +var B = 1", b)
	}
	if !file.Found || file.Path != "b.go" || file.Line != 0 || retry.Found {
		t.Errorf("b.go = %+v, retry = %+v; want b.go a changed file and retry not found", file, retry)
	}

	code, stdout, _ = lupaWith(clone, forge, "map", "7")
	for _, want := range []string{
		"**Description vs diff**",
		"- \"Adds `B` in b.go and drops `retry`.\": `B` at `b.go:3` (`+var B = 1`); `b.go` is a changed file; `retry` not found",
		"- \"Callers read the value faster.\": not checked, cites no code",
	} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
}

func TestReviewPRChecksProseClaims(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	clone, _, head := newPRClone(t)
	forge := prForge(head)
	forge.pr.Body = "Adds `B` in b.go. Callers read the value faster. Sets `cfg.limit`."
	var mu sync.Mutex
	var sent string
	ag := &agent.Fake{Reply: func(req agent.Request) (string, error) {
		if strings.Contains(req.Prompt, "For each numbered sentence") {
			mu.Lock()
			sent = req.Prompt
			mu.Unlock()
			return `{"results":[{"id":0,"claim":"Callers read the value faster","outcome":"not_judgeable"},` +
				`{"id":1,"claim":"Sets ` + "`cfg.limit`" + `","outcome":"supported","file":"b.go","quote":"var B = 1"}]}`, nil
		}
		if req.Subagents {
			return sessionReply(req.Prompt, ""), nil
		}
		m := groupPaths.FindStringSubmatch(req.Prompt)
		return `{"files_inspected": ` + m[1] + `, "comments": []}`, nil
	}}
	var out, errOut bytes.Buffer
	code := Run([]string{"review", "7", "--yes", "--json"}, Env{Dir: clone, Stdout: &out, Stderr: &errOut, Forge: forge, Agent: ag})
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(sent, "0. Callers read the value faster.\n1. Sets `cfg.limit`.\n") || strings.Contains(sent, "Adds `B`") {
		t.Errorf("claim check prompt does not carry exactly the prose and not-found-as-written sentences:\n%s", sent)
	}
	var got struct {
		Claims []struct {
			Sentence, Claim, Outcome, File, Side, Code string
			Line                                       int
		} `json:"claims"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		Sentence, Claim, Outcome, File, Side, Code string
		Line                                       int
	}{
		{"Callers read the value faster.", "Callers read the value faster", "not_judgeable", "", "", "", 0},
		{"Sets `cfg.limit`.", "Sets `cfg.limit`", "supported", "b.go", "+", "var B = 1", 3},
	}
	if !reflect.DeepEqual(got.Claims, want) {
		t.Errorf("claims = %+v, want %+v", got.Claims, want)
	}
}

func TestReviewPlanCountsTheClaimCheck(t *testing.T) {
	clone, _, head := newPRClone(t)
	runs := func(body string) int {
		forge := prForge(head)
		forge.pr.Body = body
		code, stdout, stderr := lupaWith(clone, forge, "review", "7", "--plan", "--json")
		var plan struct{ Estimate struct{ Runs int } }
		if err := json.Unmarshal([]byte(stdout), &plan); code != 0 || err != nil {
			t.Fatalf("exit %d, %v, stderr %s", code, err, stderr)
		}
		return plan.Estimate.Runs
	}
	if cited, prose := runs("Adds `B` so that callers can read the new value without parsing a file."), runs("Adds B so that callers can read the new value without parsing the text file."); prose != cited+1 {
		t.Errorf("runs = %d with a prose description, %d with every claim found; want one more for the claim check", prose, cited)
	}
}

func TestCacheCloneLeavesNothingBehindWhenItFails(t *testing.T) {
	cache := t.TempDir()
	forge := prForge("abc")
	forge.pr.Ref.Host = "127.0.0.1:1"
	var out, errOut bytes.Buffer
	code := Run([]string{"map", "https://127.0.0.1:1/acme/shop/pull/7"}, Env{Dir: t.TempDir(), Stdout: &out, Stderr: &errOut, Forge: forge, CacheDir: cache})
	if code == 0 {
		t.Fatalf("exit 0 cloning from a closed port")
	}
	left, _ := filepath.Glob(filepath.Join(cache, "*", "*", "*"))
	hidden, _ := filepath.Glob(filepath.Join(cache, "*", "*", ".*"))
	if len(left)+len(hidden) != 0 {
		t.Errorf("a failed clone left %v %v; a later run would reuse it", left, hidden)
	}
}
