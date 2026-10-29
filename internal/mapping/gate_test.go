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
