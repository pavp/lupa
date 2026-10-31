package review

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/reach"
	"github.com/pavp/lupa/internal/verify"
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
@@ -20,3 +20,3 @@ export function count(items) {
   let n = 0;
-  for (const i of items) n += 1;
+  for (const i of items) n += i.qty;
   return n;
`

const (
	taxQuote   = "  return amount * rate;"
	cartQuote  = "  for (const i of items) sum += i.price * i.qty;"
	countQuote = "  for (const i of items) n += i.qty;"
)

func runCartHead() string {
	lines := make([]string, 23)
	lines[9], lines[10], lines[11] = "  let sum = 0;", cartQuote, "  return sum;"
	lines[19], lines[20], lines[21] = "  let n = 0;", countQuote, "  return n;"
	return strings.Join(lines, "\n") + "\n"
}

var runPatches = map[string]string{"lib/tax.ts": runTaxPatch, "src/cart.ts": runCartPatch}

// runPlan plans lib/tax.ts and src/cart.ts next to a skipped generated file; with no budget it
// uses one small enough that each file gets its own group, tax first.
// twoGroups is a budget each patch fits but the whole change does not, so the plan keeps one group per directory.
const twoGroups = len(runTaxPatch) + len(runCartPatch) - 1

func runPlan(opts Options) Result {
	if opts.Budget == 0 {
		opts.Budget = 400
	}
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

// fakeRun routes each request by its system prompt: reviewer, relocation, investigator or claims.
// With no investigate reply, an investigator confirms its candidate by quoting the candidate's first line.
type fakeRun struct {
	review      func(prompt string) (string, error)
	relocate    string
	investigate func(prompt string) string
	claims      string
	mu          sync.Mutex
	reqs        []agent.Request
}

var candidateAt = regexp.MustCompile(`File: (\S+)\nLines: (\d+)-`)

func runHead(path string) (string, bool) {
	switch path {
	case "lib/tax.ts":
		return runTaxHead, true
	case "src/cart.ts":
		return runCartHead(), true
	}
	return "", false
}

func confirmCandidate(prompt string) string {
	m := candidateAt.FindStringSubmatch(prompt)
	line, _ := strconv.Atoi(m[2])
	head, _ := runHead(m[1])
	b, _ := json.Marshal(map[string]any{"verdict": "confirmed", "reasoning": "reached",
		"evidence": []Evidence{{File: m[1], Line: line, Quote: strings.Split(head, "\n")[line-1]}}})
	return string(b)
}

var runSymbols = map[string][]reach.Symbol{"lib/tax.ts": {{File: "lib/tax.ts", Name: "tax", Line: 3, End: 5, Callers: []reach.Site{{File: "src/order.ts", Line: 9}}}}}

func (f *fakeRun) input(plan Result) RunInput {
	fake := &agent.Fake{Reply: func(req agent.Request) (string, error) {
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.mu.Unlock()
		switch req.SystemPrompt {
		case relocateSystem:
			return f.relocate, nil
		case investigateSystem:
			if f.investigate == nil {
				return confirmCandidate(req.Prompt), nil
			}
			return f.investigate(req.Prompt), nil
		case claimsSystem:
			return f.claims, nil
		}
		return f.review(req.Prompt)
	}}
	return RunInput{
		Plan:        plan,
		Patches:     runPatches,
		Importers:   map[string][]string{"lib/tax.ts": {"src/order.ts"}},
		Symbols:     runSymbols,
		Conventions: []Convention{{Path: "AGENTS.md", Content: "Use tabs."}},
		Agent:       billed{fake},
		Dir:         "/tmp/worktree",
		Timeout:     2 * time.Minute,
		Head:        runHead,
		Investigate: true,
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

func TestRunPromotesWarningsWithAnObservableHarmThatAnInvestigatorConfirms(t *testing.T) {
	plan := runPlan(Options{})
	harmed := func(c map[string]any, harm string) map[string]any { c["harm"] = harm; return c }
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			return answer([]string{"lib/tax.ts", "src/cart.ts"},
				harmed(comment("lib/tax.ts", "warning", "rate may be undefined", taxQuote, false), "wrong_data"),
				harmed(comment("src/cart.ts", "warning", "qty is named poorly", cartQuote, false), "none")), nil
		},
	}
	in := f.input(plan)
	in.Investigate = false
	out := Run(context.Background(), in)

	levels := map[string]string{}
	for _, a := range out.Findings {
		levels[a.Finding.Location.File] = a.Finding.Level
	}
	if want := map[string]string{"lib/tax.ts": verify.LevelImportant, "src/cart.ts": verify.LevelNote}; !reflect.DeepEqual(levels, want) {
		t.Errorf("levels = %v, want the confirmed harm promoted and the other kept as a note", levels)
	}
	if n := f.count(investigateSystem); n != 1 {
		t.Errorf("investigator calls = %d, want 1, for the warning with an observable harm only", n)
	}
	if len(out.Findings) != 2 || len(out.Findings[0].Evidence) == 0 {
		t.Errorf("findings = %+v, want both, the promoted one with its evidence", out.Findings)
	}
}

func TestRunKeepsTheInvestigatorsReasonOnAWarningItDidNotConfirm(t *testing.T) {
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			c := comment("lib/tax.ts", "warning", "rate may be undefined", taxQuote, false)
			c["harm"] = "wrong_data"
			return answer([]string{"lib/tax.ts", "src/cart.ts"}, c), nil
		},
		investigate: func(string) string {
			return `{"verdict": "unproven", "reasoning": "no caller passes an undefined rate", "blocks": "", "evidence": []}`
		},
	}
	in := f.input(runPlan(Options{}))
	in.Investigate = false
	out := Run(context.Background(), in)

	if len(out.Findings) != 1 || out.Findings[0].Finding.Level != verify.LevelNote || !strings.Contains(out.Findings[0].Held, "no caller passes an undefined rate") {
		t.Errorf("findings = %+v, want the warning kept as a note with the investigator's reason", out.Findings)
	}
}

func TestRunWithoutInvestigationReportsEveryAnchoredCandidate(t *testing.T) {
	plan := runPlan(Options{})
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "severe", "rate may be undefined", taxQuote, false)), nil
			}
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, false)), nil
		},
	}
	in := f.input(plan)
	in.Investigate = false
	out := Run(context.Background(), in)

	if got := paths(out.Findings); !reflect.DeepEqual(got, []string{"lib/tax.ts", "src/cart.ts"}) {
		t.Fatalf("findings = %v, want both candidates", got)
	}
	if n := f.count(investigateSystem); n != 0 || out.Usage.Calls != 2 {
		t.Errorf("investigator calls = %d, total calls = %d; want none and the 2 reviewers", n, out.Usage.Calls)
	}
	if len(out.Refuted) != 0 || len(out.Unconfirmed) != 0 {
		t.Errorf("refuted %v, unconfirmed %v; want none without investigation", out.Refuted, out.Unconfirmed)
	}
}

func TestRunReviewsEveryGroupAndInvestigatesEachFinding(t *testing.T) {
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
	if f.count(systemPrompt) != 2 || f.count(investigateSystem) != 2 {
		t.Fatalf("reviewer calls = %d, investigator calls = %d, want 2 and 2", f.count(systemPrompt), f.count(investigateSystem))
	}
	if want := []Evidence{{File: "src/cart.ts", Line: 11, Quote: cartQuote}}; !reflect.DeepEqual(out.Findings[1].Evidence, want) {
		t.Errorf("cart evidence = %+v, want %+v", out.Findings[1].Evidence, want)
	}
	want := BuildPrompt(PromptInput{Patch: runTaxPatch, Paths: []string{"lib/tax.ts"},
		Importers: map[string][]string{"lib/tax.ts": {"src/order.ts"}}, Symbols: runSymbols, Conventions: []Convention{{Path: "AGENTS.md", Content: "Use tabs."}}})
	first := f.reqs[slices.IndexFunc(f.reqs, func(r agent.Request) bool { return group(r.Prompt, "lib/tax.ts") })]
	if first.SystemPrompt != want.System || first.Prompt != want.User || first.Dir != "/tmp/worktree" || first.Timeout != 2*time.Minute || first.Schema != reviewSchema {
		t.Errorf("first reviewer request = %+v, want the BuildPrompt of group 1 in the worktree with the timeout", first)
	}
	half := Tally{Calls: 2, CostUSD: 1, InputTokens: 200, OutputTokens: 20}
	wantUsage := Usage{Tally: Tally{Calls: 4, CostUSD: 2, InputTokens: 400, OutputTokens: 40}, Stages: map[string]Tally{UsageReview: half, UsageInvestigate: half}}
	if !reflect.DeepEqual(out.Usage, wantUsage) {
		t.Errorf("usage = %+v, want %+v", out.Usage, wantUsage)
	}
	if !reflect.DeepEqual(out.Ledger, plan.Ledger) {
		t.Errorf("ledger = %+v, want the plan's %+v", out.Ledger, plan.Ledger)
	}
	if len(out.Dropped) != 0 || len(out.Refuted) != 0 || len(out.Unconfirmed) != 0 {
		t.Errorf("dropped = %v, refuted = %v, unconfirmed = %v, want none", out.Dropped, out.Refuted, out.Unconfirmed)
	}
}

func TestRunListsUnconfirmedFindingsAndDoesNotInvestigateFollowUps(t *testing.T) {
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "severe", "rate import is stale", `import { rate } from "./rate";`, true)), nil
			}
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", cartQuote, false)), nil
		},
		investigate: func(string) string { return `{"verdict": "unproven", "reasoning": "no caller found", "evidence": []}` },
	}
	out := Run(context.Background(), f.input(runPlan(Options{})))

	if len(out.Findings) != 1 || out.Findings[0].Finding.Level != verify.LevelFollowUp || out.Findings[0].Evidence != nil {
		t.Fatalf("findings = %+v, want only the pre-existing follow-up, without evidence", out.Findings)
	}
	if f.count(investigateSystem) != 1 {
		t.Errorf("investigator calls = %d, want 1: the follow-up is not investigated", f.count(investigateSystem))
	}
	if len(out.Unconfirmed) != 1 || out.Unconfirmed[0].Finding.Finding.Comment.Claim != "qty may be undefined" ||
		out.Unconfirmed[0].Reason != "unproven: no caller found" {
		t.Errorf("unconfirmed = %+v, want the cart finding with its reason", out.Unconfirmed)
	}
	if out.Usage.Calls != 3 {
		t.Errorf("usage calls = %d, want 2 reviewers and 1 investigator", out.Usage.Calls)
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
		{name: "unusable reply", reply: "looks fine to me", reason: "reviewer output unusable after one retry: "},
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
			// Two reviewers and one investigator for the cart finding; an unusable answer adds its retry.
			calls := 3
			if tt.name == "unusable reply" {
				calls = 4
			}
			if out.Usage.Calls != calls {
				t.Errorf("usage calls = %d, want %d (failed runs count)", out.Usage.Calls, calls)
			}
			if plan.Ledger[1].Status != Reviewed {
				t.Errorf("the plan's ledger was modified: %+v", plan.Ledger)
			}
		})
	}
}

func TestRunReviewsGroupsInParallelAndKeepsGroupOrder(t *testing.T) {
	plan := runPlan(Options{Budget: len(runCartPatch) - 1})
	var mu sync.Mutex
	inFlight, peak := 0, 0
	f := &fakeRun{review: func(prompt string) (string, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		defer func() { mu.Lock(); inFlight--; mu.Unlock() }()
		switch {
		case group(prompt, "lib/tax.ts"):
			time.Sleep(150 * time.Millisecond)
			return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "warning", "rate may be undefined", taxQuote, false)), nil
		case strings.Contains(prompt, "@@ -10,3"):
			time.Sleep(50 * time.Millisecond)
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "warning", "qty may be undefined", cartQuote, false)), nil
		}
		return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "warning", "qty may be zero", countQuote, false)), nil
	}}
	in := f.input(plan)
	in.Parallel = 2
	out := Run(context.Background(), in)

	if peak != 2 {
		t.Errorf("peak concurrent reviewers = %d, want 2 (the limit)", peak)
	}
	var got []string
	for _, a := range out.Findings {
		got = append(got, a.Finding.Comment.Claim)
	}
	if want := []string{"rate may be undefined", "qty may be undefined", "qty may be zero"}; !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v, want group order %v", got, want)
	}
	if !reflect.DeepEqual(out.Ledger, plan.Ledger) || out.Usage.Calls != 6 {
		t.Errorf("ledger = %+v, calls = %d, want the plan's ledger and 6 calls: 3 reviewers, 3 investigators", out.Ledger, out.Usage.Calls)
	}
}

func TestRunRecomputesTheCausalityOfAMergedFinding(t *testing.T) {
	plan := runPlan(Options{})
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"},
					comment("lib/tax.ts", "severe", "rate may be undefined at the end", "}", false),
					comment("lib/tax.ts", "warning", "rate may be undefined here", taxQuote+"\n}", false)), nil
			}
			return answer([]string{"src/cart.ts"}), nil
		},
	}
	out := Run(context.Background(), f.input(plan))

	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v, want the two tax comments merged", out.Findings)
	}
	got := out.Findings[0].Finding
	if got.Location.Start != 5 || got.Location.End != 6 || got.Causality != verify.CausalityIntroduced {
		t.Errorf("survivor at %d-%d is %s, want 5-6 introduced: the widened range holds a changed line",
			got.Location.Start, got.Location.End, got.Causality)
	}
}

func TestRunTreatsANegativeParallelAsOne(t *testing.T) {
	f := &fakeRun{review: func(string) (string, error) { return answer([]string{"lib/tax.ts"}), nil }}
	in := f.input(runPlan(Options{}))
	in.Parallel = -1
	if out := Run(context.Background(), in); out.Usage.Calls != 2 {
		t.Errorf("calls = %d, want both groups reviewed", out.Usage.Calls)
	}
}

func TestRunRetriesAnUnusableAnswerOnce(t *testing.T) {
	plan := runPlan(Options{})
	taxRuns := 0
	f := &fakeRun{review: func(prompt string) (string, error) {
		if group(prompt, "lib/tax.ts") {
			taxRuns++
			if taxRuns == 1 {
				return "I checked the callers. " + answer([]string{"lib/tax.ts"}), nil
			}
			return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "warning", "rate may be undefined", taxQuote, false)), nil
		}
		return answer([]string{"src/cart.ts"}), nil
	}}
	out := Run(context.Background(), f.input(plan))

	if taxRuns != 2 {
		t.Fatalf("tax group ran %d times, want 2 (one retry)", taxRuns)
	}
	if tax := ledgerEntry(t, out.Ledger, "lib/tax.ts"); tax.Status != Reviewed {
		t.Errorf("tax entry = %+v, want reviewed after the retry", tax)
	}
	if got := paths(out.Findings); !reflect.DeepEqual(got, []string{"lib/tax.ts"}) {
		t.Errorf("findings = %v, want the retried answer's finding", got)
	}
	if out.Usage.Calls != 4 {
		t.Errorf("usage calls = %d, want 4: 3 reviewers and 1 investigator", out.Usage.Calls)
	}
}

func TestRunAsksEveryRunForItsAnswerSchema(t *testing.T) {
	plan := runPlan(Options{})
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"}, comment("lib/tax.ts", "severe", "rate may be undefined", "  return amount * rates;", false)), nil
			}
			return answer([]string{"src/cart.ts"}), nil
		},
		relocate: `{"quote": "  return amount * rate;"}`,
	}
	Run(context.Background(), f.input(plan))

	want := map[string]string{systemPrompt: reviewSchema, relocateSystem: relocateSchema, investigateSystem: investigateSchema}
	for _, r := range f.reqs {
		if !json.Valid([]byte(r.Schema)) || r.Schema != want[r.SystemPrompt] {
			t.Errorf("request with system prompt %.40q has schema %q, want its answer schema", r.SystemPrompt, r.Schema)
		}
	}
	if len(f.reqs) != 4 {
		t.Errorf("requests = %d, want 2 reviewers, 1 relocation and 1 investigator", len(f.reqs))
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
		investigate: func(string) string {
			return `{"verdict": "refuted", "reasoning": "rate is imported", "blocks": "rate being undefined at the call", "evidence": [{"file": "lib/tax.ts", "line": 1, "quote": "import { rate } from \"./rate\";"}]}`
		},
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
	if len(out.Refuted) != 1 || out.Refuted[0].Evidence[0].Line != 1 || out.Refuted[0].Finding.Finding.Comment.Claim != "rate may be negative" {
		t.Fatalf("refuted = %+v, want the tax finding with its evidence", out.Refuted)
	}
	if len(out.Findings) != 0 {
		t.Errorf("findings = %+v, want none", out.Findings)
	}
	one := Tally{Calls: 1, CostUSD: 0.5, InputTokens: 100, OutputTokens: 10}
	want := Usage{Tally: Tally{Calls: 4, CostUSD: 2, InputTokens: 400, OutputTokens: 40},
		Stages: map[string]Tally{UsageReview: {Calls: 2, CostUSD: 1, InputTokens: 200, OutputTokens: 20}, UsageAnchor: one, UsageInvestigate: one}}
	if !reflect.DeepEqual(out.Usage, want) {
		t.Errorf("usage = %+v, want %+v over two reviewers, one relocation and one investigator", out.Usage, want)
	}
}

func TestRunMergesDuplicatesAcrossGroups(t *testing.T) {
	plan := runPlan(Options{Budget: len(runCartPatch) - 1})
	if got := layout(plan); !reflect.DeepEqual(got, []string{"1:lib/tax.ts", "2:src/cart.ts@10", "3:src/cart.ts@20"}) {
		t.Fatalf("precondition: layout = %v, want the cart split over groups 2 and 3", got)
	}
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			switch {
			case group(prompt, "lib/tax.ts"):
				return answer([]string{"lib/tax.ts"}), nil
			case strings.Contains(prompt, "@@ -10,3"):
				return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "warning", "qty may be undefined here", cartQuote, true)), nil
			}
			return answer([]string{"src/cart.ts"}, comment("src/cart.ts", "severe", "qty may be undefined", "sum += qty", true)), nil
		},
		relocate: `{"quote": "` + cartQuote + `"}`,
	}
	out := Run(context.Background(), f.input(plan))

	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v, want the two cart comments merged into one", out.Findings)
	}
	got := out.Findings[0]
	if got.Finding.Merged != 1 || got.Finding.Level != verify.LevelBlocking || !got.Relocated {
		t.Errorf("survivor = %+v, want the relocated blocking comment that absorbed one", got)
	}
	if f.count(investigateSystem) != 1 {
		t.Errorf("investigator calls = %d, want 1: only the merged survivor is investigated", f.count(investigateSystem))
	}
}

func TestRunChecksTheDescriptionAlongsideTheGroups(t *testing.T) {
	f := &fakeRun{
		review: func(prompt string) (string, error) {
			if group(prompt, "lib/tax.ts") {
				return answer([]string{"lib/tax.ts"}), nil
			}
			return answer([]string{"src/cart.ts"}), nil
		},
		claims: `{"results":[{"id":0,"claim":"Tax is faster","outcome":"not_judgeable"}]}`,
	}
	in := f.input(runPlan(Options{}))
	in.Sentences = []string{"Tax is faster."}
	in.ClaimPatches = map[string]string{"lib/tax.ts": runTaxPatch}
	out := Run(context.Background(), in)
	want := []ClaimCheck{{Sentence: "Tax is faster.", Claim: "Tax is faster", Outcome: ClaimNotJudgeable}}
	if f.count(claimsSystem) != 1 || !reflect.DeepEqual(out.Claims, want) || out.Usage.Calls != 3 {
		t.Errorf("claim runs = %d, claims = %+v, calls = %d; want 1 run, %+v and 3 calls", f.count(claimsSystem), out.Claims, out.Usage.Calls, want)
	}
	if out := Run(context.Background(), f.input(runPlan(Options{}))); out.Claims != nil || f.count(claimsSystem) != 1 {
		t.Errorf("claims = %+v without sentences, want no claim run", out.Claims)
	}
}
