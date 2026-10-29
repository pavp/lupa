package report

import (
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/mapping"
)

func sample() mapping.Input {
	return mapping.Input{
		Base: "bbbbbbbbbb",
		Head: "hhhhhhhhhh",
		Files: []mapping.File{
			{Path: "README.md", Additions: 1},
			{Path: "db/migrations/001.sql", Additions: 10},
			{Path: "package-lock.json", Additions: 900},
			{Path: "src/Badge.test.ts", Additions: 5},
			{Path: "src/Badge.ts", Additions: 3, Deletions: 1},
		},
		Reach: mapping.Reach{
			Importers:  map[string][]string{"src/Badge.ts": {"src/a.ts", "src/b.ts"}},
			Unresolved: []mapping.Unresolved{{From: "src/Badge.ts", Specifier: "./missing"}},
		},
		Owners: map[string][]string{"src/Badge.ts": {"@ui"}},
	}
}

func TestMarkdown(t *testing.T) {
	want := strings.Join([]string{
		"## Lupa map — `bbbbbbb..hhhhhhh`",
		"",
		"**Review now? Yes** — 20 changed lines, generated excluded, within 400",
		"",
		"**Risk** (worst first)",
		"- Rollback: one-way door in `db/migrations/001.sql`",
		"- Impact: `src/Badge.ts` is imported by 2 files outside this change (`src/a.ts`, `src/b.ts`)",
		"- Unresolved imports: `src/Badge.ts` imports `./missing`",
		"- Verification: 1 test file changed (`src/Badge.test.ts`)",
		"",
		"**Reading order**",
		"1. Read first",
		"   - `db/migrations/001.sql`: migration or schema: hard to roll back",
		"   - `src/Badge.test.ts`: test of src/Badge.ts: read it first to know what the code must do",
		"   - `src/Badge.ts`: imported by 2 files outside this change · owners @ui",
		"2. Skim",
		"   - `README.md`: no rule matched",
		"3. Skip",
		"   - `package-lock.json`: lockfile",
		"",
		"**Questions for the author**",
		"1. `db/migrations/001.sql` is hard to roll back: what is the rollback plan?",
		"2. `src/Badge.ts` is imported by 2 files outside this change: were they checked?",
		"3. `src/Badge.ts` imports `./missing`, which is not in the repo: is a file missing from this change?",
		"",
	}, "\n")
	if got := string(Markdown(mapping.Build(sample()))); got != want {
		t.Errorf("markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarkdownHandBackStopsAfterRisk(t *testing.T) {
	in := sample()
	in.Files[4].Additions = 500
	got := string(Markdown(mapping.Build(in)))
	if !strings.Contains(got, "**Review now? Hand back** — 517 changed lines, generated excluded, over 400: suggest a split") ||
		!strings.Contains(got, `Suggested reply: "This change has 517 changed lines without generated files. Could you split it into smaller PRs?"`) {
		t.Errorf("hand-back header or suggested reply missing:\n%s", got)
	}
	if !strings.Contains(got, "**Risk**") || strings.Contains(got, "**Reading order**") || strings.Contains(got, "**Questions") {
		t.Errorf("a hand-back keeps the risk section and stops there:\n%s", got)
	}
}

func TestMarkdownNoTestsAndSinceLastLook(t *testing.T) {
	s := mapping.Build(mapping.Input{Base: "b", Head: "h", Files: []mapping.File{{Path: "src/app.ts", Additions: 2}}})
	s.SinceLastLook = &mapping.LastLook{Since: "1234567890", Files: []string{"src/app.ts"}}
	got := string(Markdown(s))
	for _, want := range []string{
		"**Since your last look** (`1234567`): 1 file changed: `src/app.ts`",
		"- Verification: no test file changed",
		"1. No test file changed: how was this change verified?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
