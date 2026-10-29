package review

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/mapping"
)

const runTaxPatch = `diff --git a/lib/tax.ts b/lib/tax.ts
--- a/lib/tax.ts
+++ b/lib/tax.ts
@@ -5,2 +5,2 @@ export function tax(amount) {
-  return amount * 0.2;
+  return amount * rate;
 }
`

const runTaxHead = "import { rate } from \"./rate\";\n\n\nexport function tax(amount) {\n  return amount * rate;\n}\n"

const runCartPatch = `diff --git a/src/cart.ts b/src/cart.ts
--- a/src/cart.ts
+++ b/src/cart.ts
@@ -10,3 +10,3 @@ export function total(items) {
   let sum = 0;
-  for (const i of items) sum += i.price;
+  for (const i of items) sum += i.price * i.qty;
   return sum;
`

const (
	taxQuote  = "  return amount * rate;"
	cartQuote = "  for (const i of items) sum += i.price * i.qty;"
)

func runCartHead() string {
	lines := make([]string, 23)
	lines[9], lines[10], lines[11] = "  let sum = 0;", cartQuote, "  return sum;"
	return strings.Join(lines, "\n") + "\n"
}

var runPatches = map[string]string{"lib/tax.ts": runTaxPatch, "src/cart.ts": runCartPatch}

// runPlan plans lib/tax.ts and src/cart.ts next to a skipped generated file; the default budget
// gives one group per file, tax first.
func runPlan(opts Options) Result {
	files := []mapping.File{
		file("lib/tax.ts"), file("src/cart.ts"),
		{Path: "gen/api.pb.ts", Class: mapping.Skip, Reason: "protobuf output"},
	}
	return Plan(files, runPatches, opts)
}

// billed adds a fixed usage to every successful reply of the fake.
type billed struct{ *agent.Fake }

func (b billed) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	resp, err := b.Fake.Run(ctx, req)
	if err == nil {
		resp.CostUSD, resp.InputTokens, resp.OutputTokens = 0.5, 100, 10
	}
	return resp, err
}

// fakeRun routes each request by its system prompt: reviewer or relocation.
type fakeRun struct {
	review   func(prompt string) (string, error)
	relocate string
	reqs     []agent.Request
}

func (f *fakeRun) input(plan Result) RunInput {
	fake := &agent.Fake{Reply: func(req agent.Request) (string, error) {
		f.reqs = append(f.reqs, req)
		if req.SystemPrompt == relocateSystem {
			return f.relocate, nil
		}
		return f.review(req.Prompt)
	}}
	return RunInput{
		Plan:        plan,
		Patches:     runPatches,
		Importers:   map[string][]string{"lib/tax.ts": {"src/order.ts"}},
		Conventions: []Convention{{Path: "AGENTS.md", Content: "Use tabs."}},
		Agent:       billed{fake},
		Dir:         "/tmp/worktree",
		Timeout:     2 * time.Minute,
		Head: func(path string) (string, bool) {
			switch path {
			case "lib/tax.ts":
				return runTaxHead, true
			case "src/cart.ts":
				return runCartHead(), true
			}
			return "", false
		},
	}
}

func (f *fakeRun) count(system string) int {
	return len(slices.DeleteFunc(slices.Clone(f.reqs), func(r agent.Request) bool { return r.SystemPrompt != system }))
}

// group reports whether a reviewer prompt is for the group of exactly these paths.
func group(prompt string, paths ...string) bool {
	b, _ := json.Marshal(paths)
	return strings.Contains(prompt, "for files_inspected: "+string(b)+"\n")
}

func answer(inspected []string, comments ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"files_inspected": inspected, "comments": append([]map[string]any{}, comments...)})
	return string(b)
}

func comment(file, severity, claim, quote string, settles bool) map[string]any {
	return map[string]any{
		"file": file, "severity": severity, "claim": claim, "quote": quote, "quote_settles": settles,
		"mechanism": "realistic input reaches it", "fix": "guard the value",
	}
}

func paths(fs []Anchored) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Finding.Location.File)
	}
	return out
}

func ledgerEntry(t *testing.T, ledger []Entry, path string) Entry {
	t.Helper()
	i := slices.IndexFunc(ledger, func(e Entry) bool { return e.Path == path })
	if i < 0 {
		t.Fatalf("no ledger entry for %s in %v", path, ledger)
	}
	return ledger[i]
}

func TestRunReviewsEveryGroup(t *testing.T) {
	plan := runPlan(Options{})
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "severe", "rate may be undefined", taxQuote, false)), nil
			}
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, false)), nil
		},
	}
	out := Run(context.Background(), f.input(plan))

	if got := paths(out.Findings); !reflect.DeepEqual(got, []string{"lib/tax.ts", "src/cart.ts"}) {
		t.Fatalf("findings = %v, want tax then cart", got)
	}
	if loc := out.Findings[1].Finding.Location; loc.Start != 11 || loc.End != 11 || loc.Side != "new" {
		t.Errorf("cart finding located at %+v, want new side line 11", loc)
	}
	if f.count(systemPrompt) != 2 || len(f.reqs) != 2 {
		t.Fatalf("reviewer calls = %d of %d, want 2 of 2", f.count(systemPrompt), len(f.reqs))
	}
	want := BuildPrompt(PromptInput{Patch: runTaxPatch, Paths: []string{"lib/tax.ts"},
		Importers: map[string][]string{"lib/tax.ts": {"src/order.ts"}}, Conventions: []Convention{{Path: "AGENTS.md", Content: "Use tabs."}}})
	first := f.reqs[0]
	if first.SystemPrompt != want.System || first.Prompt != want.User || first.Dir != "/tmp/worktree" || first.Timeout != 2*time.Minute {
		t.Errorf("first reviewer request = %+v, want the BuildPrompt of group 1 in the worktree with the timeout", first)
	}
	if wantUsage := (Usage{Calls: 2, CostUSD: 1, InputTokens: 200, OutputTokens: 20}); out.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", out.Usage, wantUsage)
	}
	if !reflect.DeepEqual(out.Ledger, plan.Ledger) {
		t.Errorf("ledger = %+v, want the plan's %+v", out.Ledger, plan.Ledger)
	}
	if len(out.Dropped) != 0 {
		t.Errorf("dropped = %v, want none", out.Dropped)
	}
}

func TestRunMarksAFailedGroupNotReviewedAndGoesOn(t *testing.T) {
	tests := []struct {
		name   string
		reply  string
		err    error
		reason string
	}{
		{name: "agent error", err: errors.New("exit status 1"), reason: "agent run failed: exit status 1"},
		{name: "unusable reply", reply: "looks fine to me", reason: "reviewer output unusable: "},
		{name: "uninspected file", reply: answer([]string{}), reason: "the reviewer did not report inspecting it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := runPlan(Options{})
			f := &fakeRun{review: func(prompt string) (string, error) {
				if group(prompt, "lib/tax.ts") {
					return tt.reply, tt.err
				}
				return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, true)), nil
			}}
			out := Run(context.Background(), f.input(plan))

			tax := ledgerEntry(t, out.Ledger, "lib/tax.ts")
			if tax.Status != NotReviewed || !strings.HasPrefix(tax.Reason, tt.reason) {
				t.Errorf("tax entry = %+v, want not-reviewed with reason %q", tax, tt.reason)
			}
			if cart := ledgerEntry(t, out.Ledger, "src/cart.ts"); cart.Status != Reviewed {
				t.Errorf("cart entry = %+v, want reviewed", cart)
			}
			if gen := ledgerEntry(t, out.Ledger, "gen/api.pb.ts"); gen.Status != Skipped {
				t.Errorf("generated entry = %+v, want the plan's skipped entry", gen)
			}
			if got := paths(out.Findings); !reflect.DeepEqual(got, []string{"src/cart.ts"}) {
				t.Errorf("findings = %v, want the cart finding of group 2", got)
			}
			if out.Usage.Calls != 2 {
				t.Errorf("usage calls = %d, want 2 (the failed run counts)", out.Usage.Calls)
			}
			if plan.Ledger[1].Status != Reviewed {
				t.Errorf("the plan's ledger was modified: %+v", plan.Ledger)
			}
		})
	}
}

func TestRunListsEveryRemovedComment(t *testing.T) {
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"},
					comment("lib/tax.ts", "critical", "rate may be undefined", taxQuote, true),
					comment("lib/tax.ts", "severe", "rate may be negative", taxQuote, false)), nil
			}
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", "  return qty ?? 0;", true)), nil
		},
		relocate: `{"quote": ""}`,
	}
	out := Run(context.Background(), f.input(runPlan(Options{})))

	if len(out.Dropped) != 2 {
		t.Fatalf("dropped = %+v, want a schema drop and an anchor drop", out.Dropped)
	}
	schema, anchor := out.Dropped[0], out.Dropped[1]
	if schema.Stage != StageSchema || schema.Reason != `severity must be severe or warning, got "critical"` || schema.Comment.File != "lib/tax.ts" {
		t.Errorf("schema drop = %+v", schema)
	}
	if anchor.Stage != StageAnchor || anchor.Reason != reasonNotFound || anchor.Comment.Claim != "qty may be undefined" {
		t.Errorf("anchor drop = %+v", anchor)
	}
	if len(out.Findings) != 1 || out.Findings[0].Finding.Comment.Claim != "rate may be negative" {
		t.Errorf("findings = %+v, want the tax finding that passed both stages", out.Findings)
	}
	if want := (Usage{Calls: 3, CostUSD: 1.5, InputTokens: 300, OutputTokens: 30}); out.Usage != want {
		t.Errorf("usage = %+v, want %+v over two reviewers and one relocation", out.Usage, want)
	}
}
