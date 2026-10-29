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
// follow-ups, the coverage and every dropped or refuted comment. No finding is capped; every
// model-written text is redacted.
func Review(in ReviewInput) []byte {
	var b strings.Builder
	out := in.Out
	fmt.Fprintf(&b, "## Lupa review — `%s..%s`\n\n", short(in.Base), short(in.Head))
	levels := map[string]int{}
	for _, f := range out.Findings {
		levels[f.Finding.Level]++
	}
	fmt.Fprintf(&b, "%s: %d blocking, %d important, %s, %s · %d dropped · %d refuted · %s, $%.2f\n\n",
		count(len(out.Findings), "finding", "findings"), levels[verify.LevelBlocking], levels[verify.LevelImportant],
		count(levels[verify.LevelNote], "note", "notes"), count(levels[verify.LevelFollowUp], "follow-up", "follow-ups"),
		len(out.Dropped), len(out.Refuted), count(out.Usage.Calls, "agent run", "agent runs"), out.Usage.CostUSD)

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
				fmt.Fprintf(&b, "- %s: `%s`: %s\n", s.title, e.Path, e.Reason)
			}
		}
	}

	if len(out.Dropped)+len(out.Refuted) > 0 {
		b.WriteString("\n### Dropped\n\n")
	}
	for _, d := range out.Dropped {
		fmt.Fprintf(&b, "- %s (group %d) · `%s`: %s", d.Stage, d.Group, d.Comment.File, d.Reason)
		if d.Comment.Claim != "" {
			fmt.Fprintf(&b, " — %s", Redact(d.Comment.Claim))
		}
		b.WriteString("\n")
	}
	for _, r := range out.Refuted {
		fmt.Fprintf(&b, "- refuted · `%s`: %s — citation `%s`\n",
			span(r.Finding.Finding.Location), Redact(r.Finding.Finding.Comment.Claim), at(r.Citation.File, r.Citation.Line))
	}
	return []byte(b.String())
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
