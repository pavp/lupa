package cli

import (
	"encoding/json"
	"strings"
	"testing"
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
	if p.Estimate.Runs != 2 || p.Estimate.Tokens != (p.Groups[0].Bytes+4000+3)/4 {
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
		"Plan: 1 groups, 2 agent runs, about ",
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
		{"no plan flag", []string{"review", "feature"}, "lupa: the agent review is not available yet; use --plan\n"},
		{"the yes flag the plan suggests", []string{"review", "feature", "--yes"}, "lupa: the agent review is not available yet; use --plan\n"},
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
