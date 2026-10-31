package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/agent/claude"
	"github.com/pavp/lupa/internal/agent/pi"
	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/verify"
)

type plan struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Budget int    `json:"budget_bytes"`
	Groups []struct {
		ID    int `json:"id"`
		Bytes int `json:"bytes"`
		Parts []struct {
			Path  string `json:"path"`
			Bytes int    `json:"bytes"`
		} `json:"parts"`
	} `json:"groups"`
	Ledger []struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Reason string `json:"reason"`
		Groups []int  `json:"groups"`
	} `json:"ledger"`
	Estimate struct {
		Runs   int `json:"runs"`
		Tokens int `json:"tokens"`
	} `json:"estimate"`
}

// newReviewRepo is newRepo plus a lockfile on the feature branch.
func newReviewRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	dir, base, _ = newRepo(t)
	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "package-lock.json", "{}\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "lock")
	head = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "main")
	return dir, base, head
}

func TestReviewPlanJSON(t *testing.T) {
	dir, base, head := newReviewRepo(t)
	code, stdout, stderr := lupa(dir, "review", "feature", "--plan", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var p plan
	if err := json.Unmarshal([]byte(stdout), &p); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if p.Schema != "lupa.plan/v0" || p.Base != base || p.Head != head || p.Budget != 60000 {
		t.Errorf("header = %q %q %q %d", p.Schema, p.Base, p.Head, p.Budget)
	}
	if len(p.Groups) != 1 || len(p.Groups[0].Parts) != 2 || p.Groups[0].Parts[0].Path != "a.txt" || p.Groups[0].Parts[1].Path != "b.go" {
		t.Fatalf("groups = %+v, want one group with a.txt and b.go", p.Groups)
	}
	got := map[string]string{}
	for _, e := range p.Ledger {
		got[e.Path] = e.Status + ": " + e.Reason
	}
	want := map[string]string{"a.txt": "reviewed: ", "b.go": "reviewed: ", "package-lock.json": "skipped: generated: lockfile"}
	if len(got) != len(want) {
		t.Errorf("ledger = %v, want %v", got, want)
	}
	for path, s := range want {
		if got[path] != s {
			t.Errorf("ledger[%s] = %q, want %q", path, got[path], s)
		}
	}
	empty := review.BuildPrompt(review.PromptInput{})
	if overhead := len(empty.System) + len(empty.User); p.Estimate.Runs != 1 || p.Estimate.Tokens != (p.Groups[0].Bytes+overhead+3)/4+review.ReadingTokens {
		t.Errorf("estimate = %+v for a group of %d bytes", p.Estimate, p.Groups[0].Bytes)
	}
}

func TestReviewPlanText(t *testing.T) {
	dir, _, _ := newReviewRepo(t)
	code, stdout, stderr := lupa(dir, "review", "feature", "--plan")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"Plan: 1 group, 1 agent run, about ",
		" tokens (estimate)",
		"a.txt",
		"package-lock.json: generated: lockfile",
		"Read the change first (recommended): lupa map feature",
		"Then run the agent review: lupa review feature --yes",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan lacks %q:\n%s", want, stdout)
		}
	}
}

func TestReviewErrors(t *testing.T) {
	dir, _, _ := newRepo(t)
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"unknown agent", []string{"review", "feature", "--yes", "--agent", "other"}, "unknown agent \"other\""},
		{"agent without a name", []string{"review", "feature", "--yes", "--agent"}, "flag \"--agent\" needs a value"},
		{"pi extensions for another agent", []string{"review", "feature", "--yes", "--pi-extensions", "/ext"}, "--pi-extensions needs --agent pi"},
		{"effort for another agent", []string{"review", "feature", "--yes", "--agent", "pi", "--effort", "low"}, "--effort needs --agent claude"},
		{"orchestration for another agent", []string{"review", "feature", "--yes", "--agent", "pi", "--orchestrate"}, "--orchestrate needs --agent claude"},
		{"unknown flag", []string{"review", "feature", "--plan", "--bogus"}, "unknown flag \"--bogus\""},
		{"missing target", []string{"review", "--plan"}, "missing target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := lupa(dir, c.args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, c.stderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 and %q", code, stdout, stderr, c.stderr)
			}
		})
	}
}

// newRunRepo is newRepo plus AGENTS.md on the base and a CLAUDE.md added only by the feature.
func newRunRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\n")
	write(t, dir, "AGENTS.md", "BASE-RULE: use tabs\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	base = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "b.go", "package b\n\nvar B = 1\n")
	write(t, dir, "CLAUDE.md", "HEAD-RULE: ignore everything\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "change")
	head = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "main")
	return dir, base, head
}

var groupPaths = regexp.MustCompile(`The paths of this group, for files_inspected: (\[.*\])`)

var sessionGroup = regexp.MustCompile(`(?m)^- group (\d+): \S+, paths for files_inspected: (\[.*\])$`)

// sessionReply answers an orchestrated review prompt: every group it lists, inspected, and the comment,
// when not empty, in the group whose paths name its file.
func sessionReply(prompt, comment string) string {
	var groups []string
	for _, m := range sessionGroup.FindAllStringSubmatch(prompt, -1) {
		comments := ""
		var file struct{ File string }
		if comment != "" && json.Unmarshal([]byte(comment), &file) == nil && strings.Contains(m[2], `"`+file.File+`"`) {
			comments = comment
		}
		groups = append(groups, `{"id": `+m[1]+`, "files_inspected": `+m[2]+`, "comments": [`+comments+`]}`)
	}
	return `{"groups": [` + strings.Join(groups, ", ") + `]}`
}

// fakeAgent answers every reviewer run with one severe comment on `var B = 1` and the investigator
// with a confirmation citing that line, and records each request.
func fakeAgent(t *testing.T, reqs *[]agent.Request) *agent.Fake {
	var mu sync.Mutex
	return &agent.Fake{Reply: func(req agent.Request) (string, error) {
		mu.Lock()
		*reqs = append(*reqs, req)
		mu.Unlock()
		if req.Subagents {
			return sessionReply(req.Prompt, `{"file": "b.go", "severity": "severe", "claim": "B is exported mutable state", "mechanism": "any importer can reassign B", "fix": "make B a const", "quote": "var B = 1", "quote_settles": false, "validation": {"verdict": "confirmed", "reasoning": "importers reassign it", "blocks": "", "evidence": [{"file": "b.go", "line": 3, "quote": "var B = 1"}]}}`), nil
		}
		if strings.Contains(req.Prompt, "Investigate the candidate defect") {
			return `{"verdict": "confirmed", "reasoning": "importers reassign it", "evidence": [{"file": "b.go", "line": 3, "quote": "var B = 1"}]}`, nil
		}
		m := groupPaths.FindStringSubmatch(req.Prompt)
		if m == nil {
			t.Errorf("unexpected request:\n%s", req.Prompt)
			return "", nil
		}
		return `{"files_inspected": ` + m[1] + `, "comments": [{"file": "b.go", "severity": "warning", "harm": "broken_caller", "claim": "B is exported mutable state", "mechanism": "any importer can reassign B", "fix": "make B a const", "quote": "var B = 1", "quote_settles": false}]}`, nil
	}}
}

func lupaRun(dir, stdin string, ag agent.Agent, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, Env{Dir: dir, Stdout: &out, Stderr: &errOut, Stdin: strings.NewReader(stdin), Agent: ag})
	return code, out.String(), errOut.String()
}

type reviewOut struct {
	Schema   string `json:"schema"`
	Base     string `json:"base"`
	Head     string `json:"head"`
	Findings []struct {
		File      string            `json:"file"`
		Side      string            `json:"side"`
		Start     int               `json:"start"`
		End       int               `json:"end"`
		Severity  string            `json:"severity"`
		Level     string            `json:"level"`
		Harm      string            `json:"harm"`
		Causality string            `json:"causality"`
		Claim     string            `json:"claim"`
		Quote     string            `json:"quote"`
		Evidence  []review.Evidence `json:"evidence"`
	} `json:"findings"`
	Refuted     []json.RawMessage `json:"refuted"`
	Unconfirmed []json.RawMessage `json:"unconfirmed"`
	Dropped     []json.RawMessage `json:"dropped"`
	Ledger      []json.RawMessage `json:"ledger"`
	Usage       struct {
		Calls  int `json:"calls"`
		Stages map[string]struct {
			Calls int `json:"calls"`
		} `json:"stages"`
	} `json:"usage"`
}

func TestReviewRunJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir, base, head := newRunRepo(t)
	var reqs []agent.Request
	code, stdout, stderr := lupaRun(dir, "", fakeAgent(t, &reqs), "review", "feature", "--yes", "--json", "--orchestrate")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var r reviewOut
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if r.Schema != "lupa.review/v0" || r.Base != base || r.Head != head {
		t.Errorf("header = %q %q %q", r.Schema, r.Base, r.Head)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %+v, want one", r.Findings)
	}
	f := r.Findings[0]
	if f.File != "b.go" || f.Side != "new" || f.Start != 3 || f.End != 3 || f.Level != "important" || f.Quote != "var B = 1" {
		t.Errorf("finding = %+v, want b.go new 3-3 important", f)
	}
	if want := []review.Evidence{{File: "b.go", Line: 3, Quote: "var B = 1"}}; !reflect.DeepEqual(f.Evidence, want) {
		t.Errorf("finding evidence = %+v, want %+v", f.Evidence, want)
	}
	if r.Refuted == nil || r.Unconfirmed == nil || r.Dropped == nil || len(r.Ledger) == 0 || r.Usage.Calls != len(reqs) || len(reqs) != 1 || !reqs[0].Subagents {
		t.Errorf("refuted %v, unconfirmed %v, dropped %v, ledger %d, usage %d, requests %d; want one orchestrated session",
			r.Refuted, r.Unconfirmed, r.Dropped, len(r.Ledger), r.Usage.Calls, len(reqs))
	}
	if got := r.Usage.Stages["orchestrate"].Calls; got != 1 {
		t.Errorf("usage stages = %+v, want the session under orchestrate", r.Usage.Stages)
	}

	saved, err := os.ReadFile(filepath.Join(dir, ".git", "lupa", "feature", head[:7], "review.json"))
	if err != nil || string(saved) != stdout {
		t.Errorf("review.json = %q, %v; want the printed JSON", saved, err)
	}
	if !strings.HasPrefix(reqs[0].Dir, tmp) {
		t.Errorf("agent ran in %q, want a worktree under %q", reqs[0].Dir, tmp)
	}
	if left, _ := filepath.Glob(filepath.Join(tmp, "lupa-review-*")); len(left) != 0 {
		t.Errorf("worktree left behind: %v", left)
	}
	if wt := gitCmd(t, dir, "worktree", "list"); strings.Count(wt, "\n") != 0 {
		t.Errorf("worktrees = %q, want only the checkout", wt)
	}
	if st, br := gitCmd(t, dir, "status", "--porcelain"), gitCmd(t, dir, "branch", "--show-current"); st != "" || br != "main" {
		t.Errorf("checkout changed: status %q, branch %q", st, br)
	}
}

func TestReviewRunPipelineByDefault(t *testing.T) {
	cases := []struct {
		name   string
		flags  []string
		stages map[string]int
	}{
		{"one reviewer run per group, no investigator", nil, map[string]int{"review": 1}},
		{"--investigate adds an investigator per finding", []string{"--investigate"}, map[string]int{"review": 1, "investigate": 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			dir, _, _ := newRunRepo(t)
			var reqs []agent.Request
			code, stdout, stderr := lupaRun(dir, "", fakeAgent(t, &reqs), append([]string{"review", "feature", "--yes", "--json"}, c.flags...)...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			var r reviewOut
			if err := json.Unmarshal([]byte(stdout), &r); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, stdout)
			}
			// The reviewer called it a warning, but an observable harm puts it in the main list.
			if len(r.Findings) != 1 || r.Findings[0].File != "b.go" || r.Findings[0].Level != "important" || r.Findings[0].Harm != "broken_caller" {
				t.Errorf("findings = %+v, want the b.go finding, important, with its harm", r.Findings)
			}
			got := map[string]int{}
			for k, v := range r.Usage.Stages {
				got[k] = v.Calls
			}
			if !reflect.DeepEqual(got, c.stages) || slices.ContainsFunc(reqs, func(q agent.Request) bool { return q.Subagents }) {
				t.Errorf("stages = %v, subagents asked: %v; want %v and no session", got, slices.ContainsFunc(reqs, func(q agent.Request) bool { return q.Subagents }), c.stages)
			}
		})
	}
}

func TestReviewRunConventionsFromBase(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir, _, _ := newRunRepo(t)
	var reqs []agent.Request
	// Pi keeps one run per group, whose prompt carries the conventions inline.
	if code, _, stderr := lupaRun(dir, "", fakeAgent(t, &reqs), "review", "feature", "--yes", "--json", "--agent", "pi"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if reqs[0].Subagents {
		t.Errorf("a Pi review asked for subagents")
	}
	if !slices.ContainsFunc(reqs, func(q agent.Request) bool { return strings.Contains(q.Prompt, "Investigate the candidate defect") }) {
		t.Errorf("a Pi review ran no investigator; Pi keeps it by default")
	}
	prompt := reqs[0].Prompt
	if !strings.Contains(prompt, "Path: AGENTS.md\n\nBASE-RULE: use tabs") {
		t.Errorf("the base AGENTS.md is not in the prompt:\n%s", prompt)
	}
	if strings.Contains(prompt, "Path: CLAUDE.md") {
		t.Errorf("a CLAUDE.md added by the change reached the conventions:\n%s", prompt)
	}
}

func TestReviewRunAsks(t *testing.T) {
	cases := []struct {
		name, stdin string
		runs        bool
	}{
		{"no", "n\n", false},
		{"end of input", "", false},
		{"yes", "y\n", true},
		{"yes spelled out", "YES\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			dir, _, _ := newRunRepo(t)
			var reqs []agent.Request
			code, stdout, stderr := lupaRun(dir, c.stdin, fakeAgent(t, &reqs), "review", "feature")
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if !strings.Contains(stdout, "Plan: ") || !strings.Contains(stdout, "Run the agent review now? [y/N] ") {
				t.Errorf("no plan and question:\n%s", stdout)
			}
			if !c.runs {
				if len(reqs) != 0 || !strings.Contains(stdout, "Not run. Read the change first: lupa map feature\n") {
					t.Errorf("agent calls %d, stdout:\n%s", len(reqs), stdout)
				}
				return
			}
			if want := "1 finding: 0 blocking, 1 important, 0 notes, 0 follow-ups · 0 dropped · 0 refuted · 0 unconfirmed · 1 agent run, $0.00\n"; len(reqs) != 1 || !strings.Contains(stdout, want) {
				t.Errorf("agent calls %d, want summary %q in:\n%s", len(reqs), want, stdout)
			}
		})
	}
}

func TestReviewRunText(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir, base, head := newRunRepo(t)
	var reqs []agent.Request
	code, stdout, stderr := lupaRun(dir, "", fakeAgent(t, &reqs), "review", "feature", "--yes")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	saveDir := filepath.Join(dir, ".git", "lupa", "feature", head[:7])
	md, err := os.ReadFile(filepath.Join(saveDir, "review.md"))
	if err != nil {
		t.Fatalf("review.md not saved: %v", err)
	}
	if !strings.HasPrefix(string(md), fmt.Sprintf("## Lupa review — `%s..%s`\n", base[:7], head[:7])) ||
		!strings.Contains(string(md), "**1. B is exported mutable state** (`b.go:3` · introduced)") {
		t.Errorf("review.md:\n%s", md)
	}
	real, err := filepath.EvalSymlinks(saveDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stdout, string(md)+"\nSaved: "+real+"\n") {
		t.Errorf("stdout does not end with the report and the saved dir %q:\n%s", real, stdout)
	}
	for _, hint := range []string{"Read the change first", "Then run the agent review", "Run the agent review now?"} {
		if strings.Contains(stdout, hint) {
			t.Errorf("--yes printed %q:\n%s", hint, stdout)
		}
	}
	if !strings.Contains(stdout, "Plan: 1 group, 1 agent run,") {
		t.Errorf("no plan:\n%s", stdout)
	}
}

func TestReviewJSONRedacts(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	c := verify.Comment{File: "a.go", Claim: "leaks " + secret, Mechanism: "token=" + secret, Fix: "drop " + secret, Quote: `password = "hunter2hunter2"`}
	a := review.Anchored{Finding: verify.Finding{Comment: c, Location: verify.Location{File: "a.go", Start: 1, End: 1}}}
	confirmed := a
	confirmed.Evidence = []review.Evidence{{File: "a.go", Line: 3, Quote: "token " + secret}}
	out := review.RunOutput{
		Findings:    []review.Anchored{confirmed},
		Refuted:     []review.Investigated{{Finding: a, Evidence: []review.Evidence{{File: "a.go", Line: 2, Quote: "key " + secret}}, Reason: "blocks: a call with " + secret}},
		Unconfirmed: []review.Investigated{{Finding: a, Reason: "investigation failed: " + secret}},
		Dropped:     []review.Drop{{Stage: review.StageAnchor, Comment: c, Reason: "relocation failed: " + secret}},
		Ledger:      []review.Entry{{Path: "b.go", Status: review.NotReviewed, Reason: "agent run failed: " + secret}},
		Notes:       []string{"filter run failed: " + secret},
	}
	b, err := json.Marshal(reviewJSONOf("b", "h", out))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "ghp_aaa") || strings.Contains(s, "hunter2") || !strings.Contains(s, `password = \"‹redacted›\"`) ||
		!strings.Contains(s, `"notes":["filter run failed: ‹redacted›"]`) {
		t.Errorf("JSON not redacted: %s", s)
	}
	for _, want := range []string{`"evidence":[{"file":"a.go","line":3,"quote":"token ‹redacted›"}]`,
		`"evidence":[{"file":"a.go","line":2,"quote":"key ‹redacted›"}],"reason":"blocks: a call with ‹redacted›"}`, `"reason":"investigation failed: ‹redacted›"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON lacks %s: %s", want, b)
		}
	}
}

func TestReviewRunExitsOneWhenNoGroupIsReviewed(t *testing.T) {
	dir, _, _ := newRunRepo(t)
	failing := &agent.Fake{Reply: func(agent.Request) (string, error) { return "", errors.New("exit status 1") }}
	code, stdout, stderr := lupaRun(dir, "", failing, "review", "feature", "--yes", "--json")

	if code != 1 || !strings.Contains(stderr, "no group was reviewed") {
		t.Fatalf("exit %d, stderr %q, want 1 and a message that no group was reviewed", code, stderr)
	}
	var got reviewOut
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || !strings.Contains(stdout, `"status": "not-reviewed"`) {
		t.Errorf("stdout is not the saved review with not-reviewed entries: %v\n%s", err, stdout)
	}
}

func TestAgentOf(t *testing.T) {
	cases := []struct {
		name, agentName, model, extensions, effort string
		want                                       agent.Agent
	}{
		{"claude by default model", "claude", "", "", "", claude.Adapter{}},
		{"claude with a model", "claude", "opus", "", "", claude.Adapter{Model: "opus"}},
		{"claude with an effort", "claude", "", "", "medium", claude.Adapter{Effort: "medium"}},
		{"pi with a model and its provider", "pi", "prov/flash", "/ext/provider, /ext/other", "", pi.Adapter{Model: "prov/flash", Extensions: []string{"/ext/provider", "/ext/other"}}},
		{"pi with no extensions", "pi", "", "", "", pi.Adapter{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := agentOf(c.agentName, c.model, c.extensions, c.effort)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("agentOf = %#v, %v; want %#v", got, err, c.want)
			}
		})
	}
	if _, err := agentOf("other", "", "", ""); err == nil || !strings.Contains(err.Error(), `unknown agent "other"`) {
		t.Errorf("err = %v, want unknown agent", err)
	}
}

func TestReviewPlanTextKeepsTheAgentFlags(t *testing.T) {
	dir, _, _ := newReviewRepo(t)
	code, stdout, stderr := lupa(dir, "review", "feature", "--plan", "--agent", "pi", "--model", "prov/flash", "--pi-extensions", "/ext/my provider")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if want := "Then run the agent review: lupa review feature --yes --agent pi --model prov/flash --pi-extensions '/ext/my provider'"; !strings.Contains(stdout, want) {
		t.Errorf("plan lacks %q:\n%s", want, stdout)
	}
}

func TestReviewPlanTextKeepsTheReviewFlags(t *testing.T) {
	dir, _, _ := newReviewRepo(t)
	code, stdout, stderr := lupa(dir, "review", "feature", "--plan", "--orchestrate", "--investigate", "--effort", "low")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if want := "Then run the agent review: lupa review feature --yes --effort low --orchestrate --investigate"; !strings.Contains(stdout, want) {
		t.Errorf("plan lacks %q:\n%s", want, stdout)
	}
}

func TestReviewRunGivesTheCallersAndCalleesOfChangedSymbols(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/util.ts", "export function slug(s: string) {\n  return s.trim();\n}\n")
	write(t, dir, "src/load.ts", "import { slug } from './util';\n\nexport function load(id: string) {\n  return id;\n}\n")
	write(t, dir, "src/page.ts", "import { load } from './load';\n\nload('a');\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "src/load.ts", "import { slug } from './util';\n\nexport function load(id: string) {\n  return slug(id);\n}\n")
	gitCmd(t, dir, "commit", "-q", "-am", "change")
	gitCmd(t, dir, "switch", "-q", "main")

	var reqs []agent.Request
	if code, _, stderr := lupaRun(dir, "", fakeAgent(t, &reqs), "review", "feature", "--yes", "--json", "--agent", "pi"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := "src/load.ts:3-5 load\n  called from: src/page.ts:3\n  calls: slug (src/util.ts:1)\n"
	if !strings.Contains(reqs[0].Prompt, want) {
		t.Errorf("the reviewer prompt lacks the changed symbol %q:\n%s", want, reqs[0].Prompt)
	}
}
