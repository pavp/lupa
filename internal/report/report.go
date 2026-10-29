// Package report renders a map as markdown, in the order a reviewer needs it.
package report

import (
	"fmt"
	"strings"

	"github.com/pavp/lupa/internal/mapping"
)

// Markdown renders the map: review now or hand back, risk worst first, reading order, questions for the
// author. A hand-back is advisory: the whole report is rendered whatever the size. Questions come only
// from deterministic signals.
func Markdown(s mapping.Signals) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "## Lupa map — `%s..%s`\n\n", short(s.Base), short(s.Head))
	if ll := s.SinceLastLook; ll != nil {
		if len(ll.Files) == 0 {
			fmt.Fprintf(&b, "**Since your last look** (`%s`): nothing changed\n\n", short(ll.Since))
		} else {
			fmt.Fprintf(&b, "**Since your last look** (`%s`): %d %s changed: %s\n\n",
				short(ll.Since), len(ll.Files), plural(len(ll.Files), "file", "files"), codes(ll.Files))
		}
	}

	if s.ReviewNow.Decision == mapping.HandBack {
		fmt.Fprintf(&b, "**Review now? Hand back** — %s\n", s.ReviewNow.Reason)
		fmt.Fprintf(&b, "Suggested reply: \"%s\"\n\n", s.ReviewNow.Reply)
	} else {
		fmt.Fprintf(&b, "**Review now? Yes** — %s\n\n", s.ReviewNow.Reason)
	}

	b.WriteString("**Risk** (worst first)\n")
	var oneWay, tests, code []string
	var oneWayAt []string
	for _, f := range s.Files {
		switch {
		case f.Risk == mapping.OneWayDoor:
			oneWay = append(oneWay, f.Path)
			oneWayAt = append(oneWayAt, at(f.Path, f.FirstLine))
		case f.Class == mapping.Test:
			tests = append(tests, f.Path)
		}
		if f.Class != mapping.Skip && f.Class != mapping.Test {
			code = append(code, f.Path)
		}
	}
	for _, p := range oneWay {
		fmt.Fprintf(&b, "- Rollback: one-way door in `%s`\n", p)
	}
	for _, f := range s.Files {
		if len(f.Importers) > 0 {
			fmt.Fprintf(&b, "- Impact: `%s` is imported by %d %s outside this change, %s\n",
				f.Path, len(f.Importers), plural(len(f.Importers), "file", "files"), directly(f))
		} else if f.Risk == mapping.Impact {
			fmt.Fprintf(&b, "- Impact: `%s` (%s)\n", f.Path, f.Reason)
		}
	}
	for _, u := range s.Unresolved {
		fmt.Fprintf(&b, "- Unresolved imports: `%s` imports `%s`\n", u.From, u.Specifier)
	}
	switch {
	case len(tests) > 0:
		fmt.Fprintf(&b, "- Verification: %d test %s changed (%s)\n", len(tests), plural(len(tests), "file", "files"), codes(tests))
	case len(code) > 0:
		b.WriteString("- Verification: no test file changed\n")
	}
	for _, m := range s.TestsNotImportingSubject {
		fmt.Fprintf(&b, "- Verification: `%s` does not import `%s`\n", m.Test, m.Subject)
	}
	b.WriteString("\n**Reading order**\n")
	n := 0
	for _, sec := range []struct {
		class mapping.Class
		title string
	}{{mapping.ReadFirst, "Read first"}, {mapping.Skim, "Skim"}, {mapping.Skip, "Skip"}} {
		var lines []string
		for _, e := range s.ReadingOrder {
			if e.Section == sec.class {
				lines = append(lines, fmt.Sprintf("   - `%s`: %s%s", e.Path, e.Why, ownersOf(s, e.Path)))
			}
		}
		if len(lines) > 0 {
			n++
			fmt.Fprintf(&b, "%d. %s\n%s\n", n, sec.title, strings.Join(lines, "\n"))
		}
	}

	var questions []string
	for _, p := range oneWayAt {
		questions = append(questions, fmt.Sprintf("`%s` is hard to roll back: what is the rollback plan?", p))
	}
	for _, f := range s.Files {
		if len(f.Importers) > 0 {
			questions = append(questions, fmt.Sprintf("`%s` is imported by %d %s outside this change: were they checked?",
				at(f.Path, f.FirstLine), len(f.Importers), plural(len(f.Importers), "file", "files")))
		}
	}
	for _, u := range s.Unresolved {
		questions = append(questions, fmt.Sprintf("`%s` imports `%s`, which is not in the repo: is a file missing from this change?", at(u.From, u.Line), u.Specifier))
	}
	for _, m := range s.TestsNotImportingSubject {
		questions = append(questions, fmt.Sprintf("`%s` is named like a test of `%s` but does not import it: does it exercise it?", at(m.Test, m.Line), m.Subject))
	}
	if len(tests) == 0 && len(code) > 0 {
		questions = append(questions, "No test file changed: how was this change verified?")
	}
	if len(questions) > 0 {
		b.WriteString("\n**Questions for the author**\n")
		for i, q := range questions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, q)
		}
	}
	if len(s.Claims) > 0 {
		b.WriteString("\n**Description vs diff**\n")
		for _, c := range s.Claims {
			fmt.Fprintf(&b, "- \"%s\": %s\n", c.Text, evidence(c))
		}
	}
	return []byte(b.String())
}

// inlineCode wraps s in a code span that survives backticks inside it.
func inlineCode(s string) string {
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

// evidence says where the diff has each thing a claim cites; a claim citing no code is not checked.
func evidence(c mapping.Claim) string {
	if len(c.Cites) == 0 {
		return "not checked, cites no code"
	}
	parts := make([]string, len(c.Cites))
	for i, ct := range c.Cites {
		switch {
		case !ct.Found:
			parts[i] = fmt.Sprintf("`%s` not found", ct.Text)
		case ct.Side == "":
			parts[i] = fmt.Sprintf("`%s` is a changed file", ct.Path)
		default:
			parts[i] = fmt.Sprintf("`%s` at `%s:%d` (%s)", ct.Text, ct.Path, ct.Line, inlineCode(ct.Side+ct.Code))
		}
	}
	return strings.Join(parts, "; ")
}

func ownersOf(s mapping.Signals, path string) string {
	for _, f := range s.Files {
		if f.Path == path && len(f.Owners) > 0 {
			return " · owners " + strings.Join(f.Owners, " ")
		}
	}
	return ""
}

// directly names the direct importers and counts the rest, so a barrel's transitive reach stays one short line.
func directly(f mapping.File) string {
	rest := len(f.Importers) - len(f.DirectImporters)
	switch {
	case len(f.DirectImporters) == 0:
		return "all through other imports"
	case rest == 0:
		return "directly by " + codes(f.DirectImporters)
	}
	return fmt.Sprintf("directly by %s; %d more through other imports", codes(f.DirectImporters), rest)
}

func codes(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "`" + it + "`"
	}
	return strings.Join(quoted, ", ")
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// at formats a file:line reference; a file without a known line stays a bare path.
func at(path string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", path, line)
	}
	return path
}
