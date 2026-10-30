package review

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/verify"
)

const itemHead = `export type Item = {
  price: number;
  qty?: number;
};
`

func candidate(claim string) Anchored {
	return Anchored{Finding: verify.Finding{
		Comment: verify.Comment{
			File: "src/cart.ts", Severity: "severe", Claim: claim,
			Mechanism: "a missing qty is undefined, so the sum becomes NaN", Quote: realQuote,
		},
		Location: verify.Location{File: "src/cart.ts", Side: "new", Start: 11, End: 11},
		Level:    verify.LevelImportant,
	}}
}

// answering replies to each investigation by the claim it carries; a claim with no answer fails the run.
func answering(answers map[string]string) *agent.Fake {
	return &agent.Fake{Reply: func(req agent.Request) (string, error) {
		for claim, reply := range answers {
			if strings.Contains(req.Prompt, "Claim: "+claim+"\n") {
				return reply, nil
			}
		}
		return "", errors.New("agent unavailable")
	}}
}

func investigateInput(ag agent.Agent, findings ...Anchored) InvestigateInput {
	return InvestigateInput{
		Agent:    ag,
		Dir:      "/tmp/worktree",
		Timeout:  2 * time.Minute,
		Findings: findings,
		Patches:  map[string]string{"src/cart.ts": cartPatch},
		Head: func(path string) (string, bool) {
			switch path {
			case "src/cart.ts":
				return cartHead, true
			case "src/item.ts":
				return itemHead, true
			}
			return "", false
		},
	}
}

func TestInvestigateDecidesEachVerdictByItsEvidence(t *testing.T) {
	fake := answering(map[string]string{
		"qty may be undefined": `{"verdict": "confirmed", "reasoning": "qty is optional", "evidence": [{"file": "src/item.ts", "line": 3, "quote": "  qty?: number;"}, {"file": "src/cart.ts", "line": 11, "quote": "` + realQuote + `"}]}`,
		"items may be null":    `{"verdict": "refuted", "reasoning": "guarded", "evidence": [{"file": "src/cart.ts", "line": 6, "quote": "  if (!items) {"}]}`,
		"invented evidence":    `{"verdict": "confirmed", "reasoning": "trust me", "evidence": [{"file": "src/item.ts", "line": 2, "quote": "  price: string;"}]}`,
		"no evidence":          `{"verdict": "confirmed", "reasoning": "obvious", "evidence": []}`,
		"restated refutation":  `{"verdict": "refuted", "reasoning": "fine", "evidence": [{"file": "src/cart.ts", "line": 11, "quote": "` + realQuote + `"}]}`,
		"cannot tell":          `{"verdict": "unproven", "reasoning": "needs runtime data", "evidence": []}`,
		"malformed":            `sure, it is a bug`,
	})
	names := []string{"qty may be undefined", "items may be null", "invented evidence", "no evidence", "restated refutation", "cannot tell", "malformed", "agent down"}
	var findings []Anchored
	for _, n := range names {
		findings = append(findings, candidate(n))
	}
	res := Investigate(context.Background(), investigateInput(fake, findings...))

	if res.Calls != len(names) || fake.Calls() != len(names) {
		t.Fatalf("calls = %d, agent calls = %d, want one per finding (%d)", res.Calls, fake.Calls(), len(names))
	}
	if len(res.Confirmed) != 1 || res.Confirmed[0].Finding.Finding.Comment.Claim != "qty may be undefined" {
		t.Fatalf("confirmed = %+v, want only the finding with evidence found in the code", res.Confirmed)
	}
	if ev := res.Confirmed[0].Evidence; len(ev) != 2 || ev[0].File != "src/item.ts" || ev[0].Line != 3 {
		t.Errorf("evidence = %+v, want both located quotes in reply order", ev)
	}
	if len(res.Refuted) != 1 || res.Refuted[0].Finding.Finding.Comment.Claim != "items may be null" || res.Refuted[0].Evidence[0].Line != 6 {
		t.Fatalf("refuted = %+v, want the guarded finding with its citation", res.Refuted)
	}
	reasons := map[string]string{}
	for _, u := range res.Unconfirmed {
		reasons[u.Finding.Finding.Comment.Claim] = u.Reason
	}
	want := map[string]string{
		"invented evidence":   "not found",
		"no evidence":         "no evidence",
		"restated refutation": "flagged code itself",
		"cannot tell":         "needs runtime data",
		"malformed":           "malformed",
		"agent down":          "agent unavailable",
	}
	if len(reasons) != len(want) {
		t.Errorf("unconfirmed = %v, want %d findings", reasons, len(want))
	}
	for claim, part := range want {
		if !strings.Contains(reasons[claim], part) {
			t.Errorf("unconfirmed %q reason = %q, want it to mention %q", claim, reasons[claim], part)
		}
	}
	var order []string
	for _, u := range res.Unconfirmed {
		order = append(order, u.Finding.Finding.Comment.Claim)
	}
	if got := strings.Join(order, ","); got != "invented evidence,no evidence,restated refutation,cannot tell,malformed,agent down" {
		t.Errorf("unconfirmed order = %q, want input order", got)
	}
}

func TestInvestigateRunsEachFindingIsolatedWithTools(t *testing.T) {
	var mu sync.Mutex
	var reqs []agent.Request
	fake := &agent.Fake{Reply: func(req agent.Request) (string, error) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		return `{"verdict": "unproven", "reasoning": "x", "evidence": []}`, nil
	}}
	Investigate(context.Background(), investigateInput(fake, candidate("qty may be undefined")))

	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Dir != "/tmp/worktree" || r.Timeout != 2*time.Minute || r.SystemPrompt != investigateSystem || r.Schema != investigateSchema {
		t.Errorf("request = %+v, want the worktree, timeout, investigator prompt and schema", r)
	}
	for _, want := range []string{"Claim: qty may be undefined\n", "a missing qty is undefined", realQuote, "+  for (const i of items) sum += i.price * i.qty;"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestInvestigateBoundsParallelRuns(t *testing.T) {
	var running, peak atomic.Int32
	fake := &agent.Fake{Reply: func(agent.Request) (string, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return `{"verdict": "unproven", "reasoning": "x", "evidence": []}`, nil
	}}
	in := investigateInput(fake, candidate("a"), candidate("b"), candidate("c"), candidate("d"), candidate("e"))
	in.Parallel = 2
	res := Investigate(context.Background(), in)

	if len(res.Unconfirmed) != 5 || peak.Load() > 2 || peak.Load() < 2 {
		t.Errorf("unconfirmed = %d, peak parallel runs = %d, want 5 and 2", len(res.Unconfirmed), peak.Load())
	}
}

func TestInvestigateWithNoFindingsMakesNoCall(t *testing.T) {
	fake := answering(nil)
	res := Investigate(context.Background(), investigateInput(fake))
	if fake.Calls() != 0 || res.Calls != 0 || len(res.Confirmed)+len(res.Refuted)+len(res.Unconfirmed) != 0 {
		t.Errorf("calls %d/%d, result %+v; want nothing", fake.Calls(), res.Calls, res)
	}
}
