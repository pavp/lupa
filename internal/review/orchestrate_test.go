package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/agent"
)

// orchestrateInput is a run of the plan over the run fixtures in a temporary worktree.
func orchestrateInput(t *testing.T, reply func(agent.Request) (string, error)) (RunInput, *agent.Fake) {
	t.Helper()
	fake := &agent.Fake{Reply: reply}
	in := (&fakeRun{}).input(runPlan(Options{}))
	in.Agent, in.Dir = fake, t.TempDir()
	return in, fake
}

// sessionAnswer is an orchestrator answer: each group by id with the comments its reviewer kept.
func sessionAnswer(groups map[int][]map[string]any, inspected map[int][]string) string {
	gs := []map[string]any{}
	for id := 1; id <= len(groups); id++ {
		gs = append(gs, map[string]any{"id": id, "files_inspected": append([]string{}, inspected[id]...), "comments": append([]map[string]any{}, groups[id]...)})
	}
	b, _ := json.Marshal(map[string]any{"groups": gs})
	return string(b)
}

func validated(c map[string]any, verdict string) map[string]any {
	c["validation"] = map[string]any{"verdict": verdict, "reasoning": "traced", "blocks": "", "evidence": []Evidence{}}
	return c
}

func TestOrchestrateRunsOneSessionOverGroupFiles(t *testing.T) {
	var req agent.Request
	in, fake := orchestrateInput(t, func(r agent.Request) (string, error) { req = r; return sessionAnswer(nil, nil), nil })
	orchestrate(context.Background(), in, in.Agent)

	if fake.Calls() != 1 || !req.Subagents || req.Schema != orchestrateSchema || req.SystemPrompt != orchestrateSystem ||
		req.Dir != in.Dir || req.Timeout != in.Timeout {
		t.Fatalf("calls = %d, request = %+v; want one session with subagents, its schema and system prompt, in the worktree", fake.Calls(), req)
	}
	for _, want := range []string{taskSection, investigateSystem, investigateOutput, `"validation"`} {
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	listed := regexp.MustCompile(`(?m)^- group (\d+): (\S+), paths for files_inspected: (\[.*\])$`).FindAllStringSubmatch(req.Prompt, -1)
	if len(listed) != len(in.Plan.Groups) {
		t.Fatalf("prompt lists %d groups, want %d", len(listed), len(in.Plan.Groups))
	}
	for i, g := range in.Plan.Groups {
		patch, paths := groupPatch(g)
		rel := listed[i][2]
		ps, _ := json.Marshal(paths)
		if listed[i][1] != strconv.Itoa(g.ID) || listed[i][3] != string(ps) || !strings.HasPrefix(rel, ".lupa-groups-") {
			t.Errorf("prompt lists group %q at %s with %s, want group %d in a fresh .lupa-groups- directory with %s", listed[i][1], rel, listed[i][3], g.ID, ps)
		}
		data, err := os.ReadFile(filepath.Join(in.Dir, rel))
		if err != nil {
			t.Fatalf("group file: %v", err)
		}
		if want := groupData(PromptInput{Patch: patch, Paths: paths, Importers: in.Importers, Symbols: in.Symbols, Conventions: in.Conventions}); string(data) != want {
			t.Errorf("group %d file = %q, want the reviewer's data blocks %q", g.ID, data, want)
		}
	}
}

func TestOrchestrateKeepsEachValidationWithItsComment(t *testing.T) {
	groups := map[int][]map[string]any{
		1: {validated(comment("lib/tax.ts", "severe", "rate may be undefined", taxQuote, false), VerdictRefuted)},
		2: {
			validated(comment("src/other.ts", "severe", "outside the group", cartQuote, false), VerdictConfirmed),
			comment("src/cart.ts", "warning", "qty may be zero", countQuote, false),
			validated(comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, false), VerdictConfirmed),
		},
	}
	in, _ := orchestrateInput(t, func(agent.Request) (string, error) {
		return sessionAnswer(groups, map[int][]string{1: {"lib/tax.ts"}, 2: {}}), nil
	})
	in.Plan = runPlan(Options{Budget: 2000})
	res := orchestrate(context.Background(), in, in.Agent)

	if len(res) != 2 || res[0].reason != "" || res[1].reason != "" {
		t.Fatalf("groups = %+v, want 2 reviewed", res)
	}
	if v := res[0].validations; len(v) != 1 || v[0] == nil || v[0].Verdict != VerdictRefuted {
		t.Errorf("tax validations = %+v, want the refuted verdict", v)
	}
	cart := res[1]
	if len(cart.parsed.Dropped) != 1 || len(cart.parsed.Comments) != 2 || len(cart.validations) != 2 {
		t.Fatalf("cart = %+v, want one dropped comment and two kept, each with its validation slot", cart)
	}
	if cart.validations[0] != nil || cart.validations[1] == nil || cart.parsed.Comments[1].Claim != "qty may be undefined" {
		t.Errorf("cart validations = %+v, want none for the unvalidated warning and the confirmation for qty", cart.validations)
	}
	if strings.Join(cart.parsed.Uninspected, ",") != "src/cart.ts" {
		t.Errorf("uninspected = %v, want src/cart.ts", cart.parsed.Uninspected)
	}
}

func TestOrchestrateRetriesOnceAndReportsWhatItCouldNotReview(t *testing.T) {
	tests := []struct {
		name    string
		replies []string
		err     error
		calls   int
		reasons []string
	}{
		{name: "unusable then fine", replies: []string{"done, see above", sessionAnswer(map[int][]map[string]any{1: nil, 2: nil}, nil)}, calls: 2, reasons: []string{"", ""}},
		{name: "unusable twice", replies: []string{"nope", `{"groups": "x"}`}, calls: 2,
			reasons: []string{"orchestrated review output unusable after one retry: ", "orchestrated review output unusable after one retry: "}},
		{name: "a group left out", replies: []string{sessionAnswer(map[int][]map[string]any{1: nil}, nil)}, calls: 1,
			reasons: []string{"", reasonGroupNotReported}},
		{name: "agent error", err: errors.New("exit status 1"), calls: 1, reasons: []string{"agent run failed: exit status 1", "agent run failed: exit status 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := 0
			in, fake := orchestrateInput(t, func(agent.Request) (string, error) {
				n++
				if tt.err != nil {
					return "", tt.err
				}
				return tt.replies[n-1], nil
			})
			in.Plan = runPlan(Options{Budget: 2000})
			res := orchestrate(context.Background(), in, in.Agent)

			if fake.Calls() != tt.calls || len(res) != len(tt.reasons) {
				t.Fatalf("calls = %d, groups = %d; want %d and %d", fake.Calls(), len(res), tt.calls, len(tt.reasons))
			}
			for i, want := range tt.reasons {
				if !strings.HasPrefix(res[i].reason, want) || want == "" && res[i].reason != "" {
					t.Errorf("group %d reason = %q, want %q", i+1, res[i].reason, want)
				}
			}
		})
	}
}

func TestRunOrchestratedDecidesEachCandidateWithTheInvestigatorsRules(t *testing.T) {
	refuted := comment("src/cart.ts", "severe", "qty may be zero", countQuote, false)
	refuted["validation"] = map[string]any{"verdict": VerdictRefuted, "reasoning": "items are validated", "blocks": "a zero qty reaching the sum",
		"evidence": []Evidence{{File: "src/cart.ts", Line: 10, Quote: "  let sum = 0;"}}}
	confirmed := comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, false)
	confirmed["validation"] = map[string]any{"verdict": VerdictConfirmed, "reasoning": "qty is optional", "blocks": "",
		"evidence": []Evidence{{File: "src/cart.ts", Line: 11, Quote: cartQuote}}}
	groups := map[int][]map[string]any{
		1: {
			comment("lib/tax.ts", "severe", "rate import is stale", `import { rate } from "./rate";`, true),
			comment("lib/tax.ts", "severe", "rate may be undefined", taxQuote, false),
			comment("lib/tax.ts", "severe", "invented code", "  return 0;", false),
		},
		2: {confirmed, refuted},
	}
	var systems []string
	fake := &agent.Fake{Reply: func(req agent.Request) (string, error) {
		systems = append(systems, req.SystemPrompt)
		if req.SystemPrompt == relocateSystem {
			return `{"quote": ""}`, nil
		}
		return sessionAnswer(groups, map[int][]string{1: {"lib/tax.ts"}, 2: {"src/cart.ts"}}), nil
	}}
	in := (&fakeRun{}).input(runPlan(Options{Budget: 2000}))
	in.Agent, in.Dir, in.Orchestrate = billed{fake}, t.TempDir(), true
	out := Run(context.Background(), in)

	if len(systems) != 2 || systems[0] != orchestrateSystem || systems[1] != relocateSystem || out.Usage.Calls != 2 {
		t.Fatalf("agent runs = %d (usage %d), want the session and one relocation, no investigator", len(systems), out.Usage.Calls)
	}
	if got := strings.Join(claims(out.Findings), ","); got != "rate import is stale,qty may be undefined" {
		t.Errorf("findings = %q, want the pre-existing follow-up and the confirmed candidate", got)
	}
	if ev := out.Findings[1].Evidence; len(ev) != 1 || ev[0].Line != 11 {
		t.Errorf("confirmed evidence = %+v, want the located quote at line 11", ev)
	}
	if len(out.Refuted) != 1 || out.Refuted[0].Reason != "blocks: a zero qty reaching the sum — items are validated" {
		t.Errorf("refuted = %+v, want qty may be zero with its blocked step", out.Refuted)
	}
	if len(out.Unconfirmed) != 1 || out.Unconfirmed[0].Finding.Finding.Comment.Claim != "rate may be undefined" || out.Unconfirmed[0].Reason != reasonNotValidated {
		t.Errorf("unconfirmed = %+v, want the candidate the session did not validate", out.Unconfirmed)
	}
	if len(out.Dropped) != 1 || out.Dropped[0].Stage != StageAnchor || out.Dropped[0].Comment.Claim != "invented code" || out.Dropped[0].Group != 1 {
		t.Errorf("dropped = %+v, want the comment whose quote is not in the change", out.Dropped)
	}
	for _, e := range out.Ledger {
		if e.Path != "gen/api.pb.ts" && e.Status != Reviewed {
			t.Errorf("ledger %+v, want every planned file reviewed", e)
		}
	}
}

func TestRunOrchestratedMarksGroupsItCouldNotReview(t *testing.T) {
	fake := &agent.Fake{Reply: func(agent.Request) (string, error) { return "", errors.New("exit status 1") }}
	in := (&fakeRun{}).input(runPlan(Options{Budget: 2000}))
	in.Agent, in.Dir, in.Orchestrate = fake, t.TempDir(), true
	out := Run(context.Background(), in)

	for _, p := range []string{"lib/tax.ts", "src/cart.ts"} {
		if e := ledgerEntry(t, out.Ledger, p); e.Status != NotReviewed || e.Reason != "agent run failed: exit status 1" {
			t.Errorf("%s = %+v, want not reviewed with the run's error", p, e)
		}
	}
}

func claims(fs []Anchored) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Finding.Comment.Claim)
	}
	return out
}

func TestOrchestrateNeverWritesThroughALinkTheChangeCommits(t *testing.T) {
	victim := t.TempDir()
	in, _ := orchestrateInput(t, func(agent.Request) (string, error) { return sessionAnswer(nil, nil), nil })
	for _, name := range []string{".lupa", ".lupa-groups"} {
		if err := os.Symlink(victim, filepath.Join(in.Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	orchestrate(context.Background(), in, in.Agent)

	if left, _ := os.ReadDir(victim); len(left) != 0 {
		t.Errorf("wrote %d entries through a committed link into %s", len(left), victim)
	}
}
