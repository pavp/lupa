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
	blocking.Evidence = []review.Evidence{{File: "src/cart.ts", Line: 5, Quote: "for (let i = 1;"}, {File: "src/sum.ts", Line: 12, Quote: "sum(items)"}}
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
			Refuted: []review.Investigated{{
				Finding:  finding(verify.LevelImportant, verify.CausalityIntroduced, "src/cart.ts", 9, 9, verify.Comment{Claim: "Null deref"}),
				Evidence: []review.Evidence{{File: "src/guard.ts", Line: 4, Quote: "if (!cart) return"}},
				Reason:   "blocks: cart reaching the sum as null — guarded at entry",
			}},
			Unconfirmed: []review.Investigated{{
				Finding: finding(verify.LevelBlocking, verify.CausalityIntroduced, "src/pay.ts", 7, 8, verify.Comment{Claim: "Total may overflow"}),
				Reason:  "unproven: no caller passes a large total",
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
		"4 findings: 1 blocking, 1 important, 1 note, 1 follow-up · 2 dropped · 1 refuted · 1 unconfirmed · 3 agent runs, $0.12",
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
		"Evidence: `src/cart.ts:5`, `src/sum.ts:12`",
		"",
		"### Important",
		"",
		"**2. Key is hardcoded** (`src/api.ts:12` · unknown)",
		"",
		"Why it matters: anyone with the repo reads the key",
		"",
		"```",
		`const api_key = "‹redacted›"`,
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
		"### Unconfirmed",
		"",
		"- blocking · `src/pay.ts:7-8`: Total may overflow — unproven: no caller passes a large total",
		"",
		"### Refuted",
		"",
		"- important · `src/cart.ts:9`: Null deref — evidence `src/guard.ts:4` — blocks: cart reaching the sum as null — guarded at entry",
		"",
		"### Dropped",
		"",
		"- schema (group 1) · `src/cart.ts`: missing field: fix",
		"- anchor (group 2) · `src/api.ts`: quote not found in the change — Wrong header",
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
		"1 finding: 1 blocking, 0 important, 0 notes, 0 follow-ups · 1 dropped · 1 refuted · 1 unconfirmed · 1 agent run, $0.04\n",
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
		"0 findings: 0 blocking, 0 important, 0 notes, 0 follow-ups · 0 dropped · 0 refuted · 0 unconfirmed · 2 agent runs, $0.02",
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

func TestReviewRedactsEveryField(t *testing.T) {
	in := sampleReview()
	in.Out.Findings = []review.Anchored{finding(verify.LevelBlocking, verify.CausalityIntroduced, "a.ts", 1, 1, verify.Comment{
		Claim: "Leaks ghp_" + strings.Repeat("a", 36), Mechanism: "logs AKIAABCDEFGHIJKLMNOP", Fix: "rotate sk-" + strings.Repeat("b", 24),
		Quote: "password: hunter2hunter2"})}
	got := string(Review(in))
	for _, leak := range []string{"ghp_aaa", "AKIAABCD", "sk-bbb", "hunter2"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "password: ‹redacted›") {
		t.Errorf("the key name is not kept:\n%s", got)
	}
}

func TestReviewShowsRunNotesAndRedactsReasons(t *testing.T) {
	secret := "ghp_" + strings.Repeat("a", 36)
	in := sampleReview()
	in.Out.Notes = []string{"filter run failed: claude: exit status 1: token " + secret}
	in.Out.Ledger = []review.Entry{{Path: "a.ts", Status: review.NotReviewed, Reason: "agent run failed: Bearer " + strings.Repeat("c", 20)}}
	in.Out.Dropped = []review.Drop{{Stage: review.StageAnchor, Group: 1, Reason: "relocation failed: key " + secret, Comment: verify.Comment{File: "a.ts"}}}
	in.Out.Unconfirmed[0].Reason = "investigation failed: token " + secret
	got := string(Review(in))
	if !strings.Contains(got, "### Run notes\n\n- filter run failed: claude: exit status 1: token ‹redacted›\n") {
		t.Errorf("the run notes are missing or not redacted:\n%s", got)
	}
	if !strings.Contains(got, "Total may overflow — investigation failed: token ‹redacted›\n") {
		t.Errorf("the unconfirmed reason is missing or not redacted:\n%s", got)
	}
	for _, leak := range []string{"ghp_aaa", "cccccccc"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked:\n%s", leak, got)
		}
	}
}

func TestReviewDescriptionVsDiff(t *testing.T) {
	got := string(Review(ReviewInput{Out: review.RunOutput{Claims: []review.ClaimCheck{
		{Sentence: "Login reads the cache.", Outcome: review.ClaimSupported, File: "src/login.ts", Side: "+", Line: 4, Code: "get(id)"},
		{Sentence: "Adds retries.", Outcome: review.ClaimUnsupported},
		{Sentence: "Login is faster.", Outcome: review.ClaimNotJudgeable},
		{Sentence: "Drops v1.", Outcome: review.ClaimNotChecked},
		{Sentence: "Adds retries to login.", Outcome: review.ClaimMentioned, File: "src/login.ts", Side: "+", Line: 2, Code: "// retries"},
	}}}))
	for _, want := range []string{
		"### Description vs diff (prose claims)\n\n",
		"- \"Login reads the cache.\": supported by `src/login.ts:4` (`+get(id)`)\n",
		"- \"Adds retries.\": no supporting line in the diff\n",
		"- \"Login is faster.\": cannot be judged from a diff\n",
		"- \"Drops v1.\": not checked\n",
		"- \"Adds retries to login.\": only mentioned in a comment or docs: `src/login.ts:2` (`+// retries`)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "verified") {
		t.Errorf("review says verified:\n%s", got)
	}
}

func TestReviewShortensALongCitedLine(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := string(Review(ReviewInput{Out: review.RunOutput{Claims: []review.ClaimCheck{
		{Sentence: "Docs say so.", Outcome: review.ClaimSupported, File: "docs/design.md", Side: "+", Line: 75, Code: long},
	}}}))
	if want := "(`+" + strings.Repeat("a", 118) + "…`)"; !strings.Contains(got, want) || strings.Contains(got, long) {
		t.Errorf("review does not shorten the cited line to 120 characters:\n%s", got)
	}
}
