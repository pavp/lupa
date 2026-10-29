package report

import (
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/verify"
)

func finding(level, causality, file string, start, end int, c verify.Comment) review.Anchored {
	c.File = file
	return review.Anchored{Finding: verify.Finding{Comment: c, Location: verify.Location{File: file, Side: "new", Start: start, End: end},
		Causality: causality, Level: level}}
}

func sampleReview() ReviewInput {
	blocking := finding(verify.LevelBlocking, verify.CausalityIntroduced, "src/cart.ts", 5, 6, verify.Comment{
		Claim: "Loop skips the first item", Mechanism: "i starts at 1, so items[0] is never summed", Fix: "Start the loop at 0",
		Quote: "for (let i = 1; i < items.length; i++) {\n  total += items[i]"})
	blocking.Relocated = true
	blocking.Finding.Merged = 2
	return ReviewInput{
		Base: "bbbbbbbbbb",
		Head: "hhhhhhhhhh",
		Out: review.RunOutput{
			Findings: []review.Anchored{
				finding(verify.LevelFollowUp, verify.CausalityPreExisting, "src/old.ts", 40, 40, verify.Comment{
					Claim: "Retry never stops", Mechanism: "the loop has no limit", Fix: "Cap the retries", Quote: "while (true) retry()"}),
				blocking,
				finding(verify.LevelImportant, verify.CausalityUnknown, "src/api.ts", 12, 12, verify.Comment{
					Claim: "Key is hardcoded", Mechanism: "anyone with the repo reads the key", Fix: "Read it from the environment",
					Quote: `const api_key = "abcd1234efgh"`}),
				finding(verify.LevelNote, verify.CausalityBehaviorActivated, "src/ui.ts", 3, 3, verify.Comment{
					Claim: "Label is now shown", Mechanism: "the flag defaults to true", Fix: "Default the flag to false", Quote: "show: true"}),
			},
			Refuted: []review.RefutedFinding{{
				Finding:  finding(verify.LevelImportant, verify.CausalityIntroduced, "src/cart.ts", 9, 9, verify.Comment{Claim: "Null deref"}),
				Citation: review.Citation{File: "src/guard.ts", Line: 4, Quote: "if (!cart) return"},
			}},
			Dropped: []review.Drop{
				{Stage: review.StageSchema, Group: 1, Reason: "missing field: fix", Comment: verify.Comment{File: "src/cart.ts"}},
				{Stage: review.StageAnchor, Group: 2, Reason: "quote not found in the change", Comment: verify.Comment{File: "src/api.ts", Claim: "Wrong header"}},
			},
			Ledger: []review.Entry{
				{Path: "package-lock.json", Status: review.Skipped, Reason: "generated: lockfile"},
				{Path: "src/api.ts", Status: review.Reviewed, Groups: []int{2}},
				{Path: "src/big.ts", Status: review.NotReviewed, Reason: "hunk at line 1 exceeds the prompt budget (70000 bytes > 60000)"},
				{Path: "src/cart.ts", Status: review.Reviewed, Groups: []int{1}},
				{Path: "src/old.ts", Status: review.Reviewed, Groups: []int{1}},
				{Path: "src/ui.ts", Status: review.Reviewed, Groups: []int{2}},
			},
			Usage: review.Usage{Calls: 3, CostUSD: 0.1234},
		},
	}
}

func TestReview(t *testing.T) {
	want := strings.Join([]string{
		"## Lupa review — `bbbbbbb..hhhhhhh`",
		"",
		"4 findings: 1 blocking, 1 important, 1 note, 1 follow-up · 2 dropped · 1 refuted · 3 agent runs, $0.12",
		"",
		"### Blocking",
		"",
		"**1. Loop skips the first item** (`src/cart.ts:5-6` · introduced · relocated · merged 2)",
		"",
		"Why it matters: i starts at 1, so items[0] is never summed",
		"",
		"```",
		"for (let i = 1; i < items.length; i++) {",
		"  total += items[i]",
		"```",
		"",
		"Fix: Start the loop at 0",
		"",
		"### Important",
		"",
		"**2. Key is hardcoded** (`src/api.ts:12` · unknown)",
		"",
		"Why it matters: anyone with the repo reads the key",
		"",
		"```",
		`const api_key = "abcd1234efgh"`,
		"```",
		"",
		"Fix: Read it from the environment",
		"",
		"### Notes",
		"",
		"**3. Label is now shown** (`src/ui.ts:3` · behavior-activated)",
		"",
		"Why it matters: the flag defaults to true",
		"",
		"```",
		"show: true",
		"```",
		"",
		"Fix: Default the flag to false",
		"",
		"### Follow-up (pre-existing)",
		"",
		"**4. Retry never stops** (`src/old.ts:40` · pre-existing)",
		"",
		"Why it matters: the loop has no limit",
		"",
		"```",
		"while (true) retry()",
		"```",
		"",
		"Fix: Cap the retries",
		"",
		"### Coverage",
		"",
		"4 files reviewed, 1 skipped, 1 not reviewed",
		"",
		"- Skipped: `package-lock.json`: generated: lockfile",
		"- Not reviewed: `src/big.ts`: hunk at line 1 exceeds the prompt budget (70000 bytes > 60000)",
		"",
		"### Dropped",
		"",
		"- schema (group 1) · `src/cart.ts`: missing field: fix",
		"- anchor (group 2) · `src/api.ts`: quote not found in the change — Wrong header",
		"- refuted · `src/cart.ts:9`: Null deref — citation `src/guard.ts:4`",
		"",
	}, "\n")
	if got := string(Review(sampleReview())); got != want {
		t.Errorf("Review() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestReviewSingular(t *testing.T) {
	in := sampleReview()
	in.Out.Findings = in.Out.Findings[1:2]
	in.Out.Dropped = in.Out.Dropped[:1]
	in.Out.Ledger = in.Out.Ledger[3:4]
	in.Out.Usage = review.Usage{Calls: 1, CostUSD: 0.04}
	got := string(Review(in))
	for _, want := range []string{
		"1 finding: 1 blocking, 0 important, 0 notes, 0 follow-ups · 1 dropped · 1 refuted · 1 agent run, $0.04\n",
		"1 file reviewed, 0 skipped, 0 not reviewed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestReviewNoFindings(t *testing.T) {
	in := sampleReview()
	in.Out = review.RunOutput{Ledger: in.Out.Ledger[:3], Usage: review.Usage{Calls: 2, CostUSD: 0.02}}
	want := strings.Join([]string{
		"## Lupa review — `bbbbbbb..hhhhhhh`",
		"",
		"0 findings: 0 blocking, 0 important, 0 notes, 0 follow-ups · 0 dropped · 0 refuted · 2 agent runs, $0.02",
		"",
		"No findings.",
		"",
		"### Coverage",
		"",
		"1 file reviewed, 1 skipped, 1 not reviewed",
		"",
		"- Skipped: `package-lock.json`: generated: lockfile",
		"- Not reviewed: `src/big.ts`: hunk at line 1 exceeds the prompt budget (70000 bytes > 60000)",
		"",
	}, "\n")
	if got := string(Review(in)); got != want {
		t.Errorf("Review() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
