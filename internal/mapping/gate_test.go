package mapping

import (
	"strings"
	"testing"
)

func TestReviewNowGate(t *testing.T) {
	cases := []struct {
		name     string
		lines    int
		decision Decision
		reason   string
	}{
		{"at the budget", 400, Review, "400 changed lines"},
		{"over the budget", 401, HandBack, "suggest a split"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Build(Input{Base: "b", Head: "h", Files: []File{
				{Path: "src/app.ts", Additions: c.lines},
				{Path: "package-lock.json", Additions: 5000},
			}})
			if s.ReviewNow.Decision != c.decision || !strings.Contains(s.ReviewNow.Reason, c.reason) {
				t.Errorf("review_now = %+v, want %q with reason containing %q", s.ReviewNow, c.decision, c.reason)
			}
		})
	}
}

func TestOwnersAreCopiedPerFile(t *testing.T) {
	s := Build(Input{Files: []File{{Path: "a.go"}, {Path: "b.go"}}, Owners: map[string][]string{"a.go": {"@team"}}})
	if strings.Join(s.Files[0].Owners, ",") != "@team" || s.Files[1].Owners != nil {
		t.Errorf("owners = %v / %v, want @team / none", s.Files[0].Owners, s.Files[1].Owners)
	}
}

func TestReviewNowGateForAPR(t *testing.T) {
	files := []File{{Path: "src/a.ts", Additions: 10}, {Path: "src/b.ts", Additions: 10}, {Path: "src/c.ts", Additions: 10}, {Path: "package-lock.json", Additions: 900}}
	purpose := "Totals now include the quantity of each item, so carts with several units charge correctly."
	cases := []struct {
		name     string
		pr       *PR
		lines    int
		decision Decision
		reason   string
	}{
		{"another open PR changes most of the same files", &PR{Number: 7, Body: purpose, Open: []OpenPR{
			{Number: 7, Files: []string{"src/a.ts", "src/b.ts", "src/c.ts"}},
			{Number: 9, Title: "Cart fix", Files: []string{"src/a.ts", "src/b.ts", "README.md"}},
		}}, 0, HandBack, "PR #9 (Cart fix) changes 2 of the same 3 files: possible duplicate"},
		{"PRs stacked on each other are not duplicates", &PR{Number: 7, Body: purpose, BaseRef: "feature", HeadRef: "feature-2", Open: []OpenPR{
			{Number: 5, HeadRef: "feature", Files: []string{"src/a.ts", "src/b.ts", "src/c.ts"}},
			{Number: 8, BaseRef: "feature-2", Files: []string{"src/a.ts", "src/b.ts", "src/c.ts"}},
		}}, 0, Review, "within 400"},
		{"an open PR sharing one file in three is not a duplicate", &PR{Number: 7, Body: purpose, Open: []OpenPR{
			{Number: 9, Files: []string{"src/a.ts", "package-lock.json"}},
		}}, 0, Review, "within 400"},
		{"a duplicate wins over size", &PR{Number: 7, Body: purpose, Open: []OpenPR{{Number: 9, Files: []string{"src/a.ts", "src/b.ts"}}}}, 500, HandBack, "possible duplicate"},
		{"size wins over intent", &PR{Number: 7}, 500, HandBack, "suggest a split"},
		{"no description", &PR{Number: 7, Body: "<!-- describe your change -->\n## Summary\n- [ ] tests\n", LinkedIssues: 1}, 0, HandBack, "no description"},
		{"no linked issue and no stated purpose", &PR{Number: 7, Body: "fix stuff"}, 0, HandBack, "no linked issue and no stated purpose"},
		{"a short description with a linked issue", &PR{Number: 7, Body: "Closes #12", LinkedIssues: 1}, 0, Review, "within 400"},
		{"a stated purpose without an issue", &PR{Number: 7, Body: purpose}, 0, Review, "within 400"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := append([]File{}, files...)
			fs[0].Additions += c.lines
			s := Build(Input{Base: "b", Head: "h", Files: fs, PR: c.pr})
			if s.ReviewNow.Decision != c.decision || !strings.Contains(s.ReviewNow.Reason, c.reason) {
				t.Errorf("review_now = %+v, want %q with reason containing %q", s.ReviewNow, c.decision, c.reason)
			}
			if c.decision == HandBack && s.ReviewNow.Reply == "" {
				t.Error("a hand-back has no suggested reply")
			}
		})
	}
}
