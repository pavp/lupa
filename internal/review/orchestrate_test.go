package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	for _, g := range in.Plan.Groups {
		patch, paths := groupPatch(g)
		rel := filepath.Join(".lupa", "groups", "g"+strconv.Itoa(g.ID)+".txt")
		ps, _ := json.Marshal(paths)
		if !strings.Contains(req.Prompt, "group "+strconv.Itoa(g.ID)+": "+rel+", paths for files_inspected: "+string(ps)) {
			t.Errorf("prompt does not list group %d at %s with its paths", g.ID, rel)
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
