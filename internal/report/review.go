package report

import (
	"fmt"
	"strings"

	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/verify"
)

// ReviewInput is a verified review of the change from Base to Head, with the change's map when there is one.
type ReviewInput struct {
	Base, Head string
	Out        review.RunOutput
	Map        *mapping.Signals
}

// Review renders a verified review in the order a reviewer needs it: whether to review now, the description
// against the diff, the findings to check (blocking and important) in full, a reading order that starts with the
// files those findings are in, the minor findings one line each, the coverage, and every unconfirmed, refuted or
// dropped comment with its reason. No finding is capped; every model-written text and every error reason is redacted.
func Review(in ReviewInput) []byte {
	var b strings.Builder
	out := in.Out
	fmt.Fprintf(&b, "## Lupa review — `%s..%s`\n\n", short(in.Base), short(in.Head))
	if in.Map != nil {
		writeReviewNow(&b, in.Map.ReviewNow)
	}
	levels := map[string]int{}
	for _, f := range out.Findings {
		levels[f.Finding.Level]++
	}
	fmt.Fprintf(&b, "%s: %d blocking, %d important, %s, %s · %d dropped · %d refuted · %d unconfirmed · %s, $%.2f\n\n",
		count(len(out.Findings), "finding", "findings"), levels[verify.LevelBlocking], levels[verify.LevelImportant],
		count(levels[verify.LevelNote], "note", "notes"), count(levels[verify.LevelFollowUp], "follow-up", "follow-ups"),
		len(out.Dropped), len(out.Refuted), len(out.Unconfirmed), count(out.Usage.Calls, "agent run", "agent runs"), out.Usage.CostUSD)

	if len(out.Claims) > 0 {
		b.WriteString("### Description vs diff (prose claims)\n\n")
		for _, c := range out.Claims {
			fmt.Fprintf(&b, "- \"%s\": %s\n", Redact(c.Sentence), claimOutcome(c))
		}
		b.WriteString("\n")
	}

	if len(out.Findings) == 0 {
		b.WriteString("No findings.\n\n")
	}
	main := map[string]int{}
	if levels[verify.LevelBlocking]+levels[verify.LevelImportant] > 0 {
		fmt.Fprintf(&b, "### Check these (%d blocking, %d important)\n\n", levels[verify.LevelBlocking], levels[verify.LevelImportant])
		n := 0
		for _, level := range []string{verify.LevelBlocking, verify.LevelImportant} {
			for _, a := range out.Findings {
				if a.Finding.Level == level {
					n++
					writeFinding(&b, n, a)
					main[a.Finding.Location.File]++
				}
			}
		}
	}
	if in.Map != nil {
		writeReadingOrder(&b, in.Map.ReadingOrder, main, out.Findings)
	}
	if levels[verify.LevelNote]+levels[verify.LevelFollowUp] > 0 {
		b.WriteString("### Minor (one line each; the JSON has the details)\n\n")
		for _, level := range []string{verify.LevelNote, verify.LevelFollowUp} {
			for _, a := range out.Findings {
				if f := a.Finding; f.Level == level {
					fmt.Fprintf(&b, "- %s · `%s` · %s: %s", f.Level, span(f.Location), f.Causality, Redact(f.Comment.Claim))
					if a.Held != "" {
						fmt.Fprintf(&b, " — not promoted: %s", Redact(a.Held))
					}
					b.WriteString("\n")
				}
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("### Coverage\n\n")
	files := map[review.Status]map[string]bool{review.Reviewed: {}, review.Skipped: {}, review.NotReviewed: {}}
	for _, e := range out.Ledger {
		files[e.Status][e.Path] = true
	}
	fmt.Fprintf(&b, "%s reviewed, %d skipped, %d not reviewed\n",
		count(len(files[review.Reviewed]), "file", "files"), len(files[review.Skipped]), len(files[review.NotReviewed]))
	gaps := false
	for _, s := range []struct {
		status review.Status
		title  string
	}{{review.Skipped, "Skipped"}, {review.NotReviewed, "Not reviewed"}} {
		for _, e := range out.Ledger {
			if e.Status == s.status {
				if !gaps {
					b.WriteString("\n")
					gaps = true
				}
				fmt.Fprintf(&b, "- %s: `%s`: %s\n", s.title, e.Path, Redact(e.Reason))
			}
		}
	}

	if len(out.Unconfirmed)+len(out.Refuted)+len(out.Dropped) > 0 {
		b.WriteString("\n### Discarded or not confirmed\n")
	}
	if len(out.Unconfirmed) > 0 {
		b.WriteString("\nUnconfirmed:\n\n")
	}
	for _, u := range out.Unconfirmed {
		f := u.Finding.Finding
		fmt.Fprintf(&b, "- %s · `%s`: %s — %s\n", f.Level, span(f.Location), Redact(f.Comment.Claim), Redact(u.Reason))
	}
	if len(out.Refuted) > 0 {
		b.WriteString("\nRefuted:\n\n")
	}
	for _, r := range out.Refuted {
		f := r.Finding.Finding
		fmt.Fprintf(&b, "- %s · `%s`: %s — evidence `%s`", f.Level, span(f.Location), Redact(f.Comment.Claim), at(r.Evidence[0].File, r.Evidence[0].Line))
		if r.Reason != "" {
			fmt.Fprintf(&b, " — %s", Redact(r.Reason))
		}
		b.WriteString("\n")
	}
	if len(out.Dropped) > 0 {
		b.WriteString("\nDropped:\n\n")
	}
	for _, d := range out.Dropped {
		fmt.Fprintf(&b, "- %s (group %d) · `%s`: %s", d.Stage, d.Group, d.Comment.File, Redact(d.Reason))
		if d.Comment.Claim != "" {
			fmt.Fprintf(&b, " — %s", Redact(d.Comment.Claim))
		}
		b.WriteString("\n")
	}
	if len(out.Notes) > 0 {
		b.WriteString("\n### Run notes\n\n")
	}
	for _, n := range out.Notes {
		fmt.Fprintf(&b, "- %s\n", Redact(n))
	}
	return []byte(b.String())
}

func writeReviewNow(b *strings.Builder, g mapping.Gate) {
	if g.Decision == mapping.HandBack {
		fmt.Fprintf(b, "**Review now? Hand back** — %s\nSuggested reply: \"%s\"\n\n", g.Reason, g.Reply)
		return
	}
	fmt.Fprintf(b, "**Review now? Yes** — %s\n\n", g.Reason)
}

// writeReadingOrder lists the files to read: first those with findings to check, in the order of their first
// finding, then the map's read-first and skim files; skipped files are in the coverage.
func writeReadingOrder(b *strings.Builder, order []mapping.ReadingEntry, main map[string]int, findings []review.Anchored) {
	why := map[string]string{}
	for _, e := range order {
		why[e.Path] = e.Why
	}
	var lines []string
	seen := map[string]bool{}
	for _, level := range []string{verify.LevelBlocking, verify.LevelImportant} {
		for _, a := range findings {
			if p := a.Finding.Location.File; a.Finding.Level == level && !seen[p] {
				seen[p] = true
				line := fmt.Sprintf("`%s`: %d to check", p, main[p])
				if w := why[p]; w != "" {
					line += " · " + w
				}
				lines = append(lines, line)
			}
		}
	}
	for _, section := range []mapping.Class{mapping.ReadFirst, mapping.Skim} {
		for _, e := range order {
			if e.Section == section && !seen[e.Path] {
				seen[e.Path] = true
				lines = append(lines, fmt.Sprintf("`%s`: %s", e.Path, e.Why))
			}
		}
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("### Reading order\n\n")
	for i, l := range lines {
		fmt.Fprintf(b, "%d. %s\n", i+1, l)
	}
	b.WriteString("\n")
}

// citedLineLimit is the most characters of a cited line the text report shows; the JSON keeps it whole.
const citedLineLimit = 120

// shorten cuts s to limit characters, the last one an ellipsis.
func shorten(s string, limit int) string {
	if r := []rune(s); len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}

// claimOutcome says what the diff shows for a claim. A cited line supports a claim; it does not prove it.
func claimOutcome(c review.ClaimCheck) string {
	switch c.Outcome {
	case review.ClaimSupported:
		return fmt.Sprintf("supported by `%s` (%s)", at(c.File, c.Line), inlineCode(shorten(Redact(c.Side+c.Code), citedLineLimit)))
	case review.ClaimMentioned:
		return fmt.Sprintf("only mentioned in a comment or docs: `%s` (%s)", at(c.File, c.Line), inlineCode(shorten(Redact(c.Side+c.Code), citedLineLimit)))
	case review.ClaimUnsupported:
		return "no supporting line in the diff"
	case review.ClaimNotJudgeable:
		return "cannot be judged from a diff"
	}
	return "not checked"
}

func writeFinding(b *strings.Builder, n int, a review.Anchored) {
	f, c := a.Finding, a.Finding.Comment
	tags := []string{"`" + span(f.Location) + "`", f.Causality}
	if a.Relocated {
		tags = append(tags, "relocated")
	}
	if f.Merged > 0 {
		tags = append(tags, fmt.Sprintf("merged %d", f.Merged))
	}
	quote := Redact(c.Quote)
	fence := "```"
	for strings.Contains(quote, fence) {
		fence += "`"
	}
	fmt.Fprintf(b, "**%d. %s** (%s)\n\nWhy it matters: %s\n\n%s\n%s\n%s\n\nFix: %s\n\n",
		n, Redact(c.Claim), strings.Join(tags, " · "), Redact(c.Mechanism), fence, quote, fence, Redact(c.Fix))
	if len(a.Evidence) > 0 {
		locs := make([]string, len(a.Evidence))
		for i, e := range a.Evidence {
			locs[i] = "`" + at(e.File, e.Line) + "`"
		}
		fmt.Fprintf(b, "Evidence: %s\n\n", strings.Join(locs, ", "))
	}
	if a.Held != "" {
		fmt.Fprintf(b, "Not promoted: %s\n\n", Redact(a.Held))
	}
}

// span formats a location as file:start, or file:start-end when it covers several lines.
func span(l verify.Location) string {
	if l.End > l.Start {
		return fmt.Sprintf("%s:%d-%d", l.File, l.Start, l.End)
	}
	return at(l.File, l.Start)
}

func count(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, plural(n, one, many))
}
