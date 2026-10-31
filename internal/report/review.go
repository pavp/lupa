package report

import (
	"fmt"
	"strings"

	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/verify"
)

// ReviewInput is a verified review of the change from Base to Head.
type ReviewInput struct {
	Base, Head string
	Out        review.RunOutput
}

// Review renders a verified review: every finding by level, worst first, then the pre-existing
// follow-ups, the coverage, every unconfirmed, refuted or dropped comment and the run's notes. No finding is
// capped; every model-written text and every error reason is redacted.
func Review(in ReviewInput) []byte {
	var b strings.Builder
	out := in.Out
	fmt.Fprintf(&b, "## Lupa review — `%s..%s`\n\n", short(in.Base), short(in.Head))
	levels := map[string]int{}
	for _, f := range out.Findings {
		levels[f.Finding.Level]++
	}
	fmt.Fprintf(&b, "%s: %d blocking, %d important, %s, %s · %d dropped · %d refuted · %d unconfirmed · %s, $%.2f\n\n",
		count(len(out.Findings), "finding", "findings"), levels[verify.LevelBlocking], levels[verify.LevelImportant],
		count(levels[verify.LevelNote], "note", "notes"), count(levels[verify.LevelFollowUp], "follow-up", "follow-ups"),
		len(out.Dropped), len(out.Refuted), len(out.Unconfirmed), count(out.Usage.Calls, "agent run", "agent runs"), out.Usage.CostUSD)

	if len(out.Findings) == 0 {
		b.WriteString("No findings.\n\n")
	}
	n := 0
	for _, sec := range []struct{ level, title string }{
		{verify.LevelBlocking, "Blocking"}, {verify.LevelImportant, "Important"}, {verify.LevelNote, "Notes"},
		{verify.LevelFollowUp, "Follow-up (pre-existing)"},
	} {
		if levels[sec.level] == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", sec.title)
		for _, a := range out.Findings {
			if a.Finding.Level == sec.level {
				n++
				writeFinding(&b, n, a)
			}
		}
	}

	if len(out.Claims) > 0 {
		b.WriteString("### Description vs diff (prose claims)\n\n")
		for _, c := range out.Claims {
			fmt.Fprintf(&b, "- \"%s\": %s\n", Redact(c.Sentence), claimOutcome(c))
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

	if len(out.Unconfirmed) > 0 {
		b.WriteString("\n### Unconfirmed\n\n")
	}
	for _, u := range out.Unconfirmed {
		f := u.Finding.Finding
		fmt.Fprintf(&b, "- %s · `%s`: %s — %s\n", f.Level, span(f.Location), Redact(f.Comment.Claim), Redact(u.Reason))
	}
	if len(out.Refuted) > 0 {
		b.WriteString("\n### Refuted\n\n")
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
		b.WriteString("\n### Dropped\n\n")
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
