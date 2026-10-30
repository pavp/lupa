package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/verify"
)

const scoreCartPatch = "diff --git a/src/cart.ts b/src/cart.ts\n--- a/src/cart.ts\n+++ b/src/cart.ts\n@@ -1,1 +1,1 @@\n-total = 0\n+total = sum(items)\n"

func scored(file, claim string, start int) Anchored {
	return Anchored{Finding: verify.Finding{
		Comment:  verify.Comment{File: file, Severity: "warning", Claim: claim, Mechanism: "an empty cart", Quote: "total = sum(items)"},
		Location: verify.Location{File: file, Side: "new", Start: start, End: start},
		Level:    verify.LevelNote,
	}}
}

func scoreInput(reply string, err error, reqs *[]agent.Request) ScoreInput {
	return ScoreInput{
		Agent: &agent.Fake{Reply: func(r agent.Request) (string, error) {
			*reqs = append(*reqs, r)
			return reply, err
		}},
		Dir:       "/tmp/worktree",
		Model:     "a-cheaper-model",
		Threshold: 7,
		Findings:  []Anchored{scored("src/cart.ts", "total is NaN for an empty cart", 1), scored("src/cart.ts", "add a docstring", 1)},
		Patches:   map[string]string{"src/cart.ts": scoreCartPatch, "src/other.ts": "diff --git a/src/other.ts b/src/other.ts\n"},
	}
}

func TestScoreSplitsFindingsAtTheThreshold(t *testing.T) {
	var reqs []agent.Request
	res := Score(context.Background(), scoreInput(`{"results":[{"id":0,"score":8,"reason":"sum of nothing"},{"id":1,"score":0,"reason":"a docstring"}]}`, nil, &reqs))
	if len(res.Kept) != 1 || res.Kept[0].Finding.Comment.Claim != "total is NaN for an empty cart" {
		t.Errorf("kept = %+v, want the defect only", res.Kept)
	}
	if len(res.Low) != 1 || res.Low[0].Score != 0 || res.Low[0].Reason != "a docstring" || res.Low[0].Finding.Finding.Comment.Claim != "add a docstring" {
		t.Errorf("low = %+v, want the docstring with score 0 and its reason", res.Low)
	}
	if res.Calls != 1 || len(reqs) != 1 || res.Note != "" {
		t.Fatalf("calls %d, requests %d, note %q; want one clean call", res.Calls, len(reqs), res.Note)
	}
	r := reqs[0]
	if !r.NoTools || r.Model != "a-cheaper-model" || r.Schema != scoreSchema || r.Dir != "/tmp/worktree" || r.SystemPrompt != scoreSystem {
		t.Errorf("request = %+v, want a tool-free call with the scorer model and schema", r)
	}
	for _, want := range []string{"0 to 10", "8-10", "ID: 0", "ID: 1", "total is NaN for an empty cart", "+total = sum(items)"} {
		if !strings.Contains(r.Prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(r.Prompt, "src/other.ts") {
		t.Errorf("prompt carries the patch of a file no finding is about")
	}
}

func TestScoreKeepsEveryFindingWhenItCannotScore(t *testing.T) {
	cases := []struct {
		name, reply string
		err         error
		note        string
	}{
		{"the run fails", "", errors.New("timeout"), "scorer run failed: timeout"},
		{"a malformed reply", "sure!", nil, "scorer reply malformed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var reqs []agent.Request
			res := Score(context.Background(), scoreInput(c.reply, c.err, &reqs))
			if len(res.Kept) != 2 || len(res.Low) != 0 || !strings.Contains(res.Note, c.note) {
				t.Errorf("kept %d, low %d, note %q; want both kept and %q", len(res.Kept), len(res.Low), res.Note, c.note)
			}
		})
	}
}

func TestScoreKeepsAFindingItGotNoValidScoreFor(t *testing.T) {
	var reqs []agent.Request
	res := Score(context.Background(), scoreInput(`{"results":[{"id":0,"score":12,"reason":"x"},{"id":9,"score":1,"reason":"z"}]}`, nil, &reqs))
	if len(res.Kept) != 2 || len(res.Low) != 0 {
		t.Errorf("kept %d, low %d; want both kept: id 0 has no valid score, id 1 none at all", len(res.Kept), len(res.Low))
	}
	for _, want := range []string{"id 0: score 12 out of range", "id 9: unknown", "id 1: no score, kept"} {
		if !strings.Contains(strings.Join(res.Notes, "\n"), want) {
			t.Errorf("notes %q lack %q", res.Notes, want)
		}
	}
}

func TestScoreWithoutFindingsMakesNoCall(t *testing.T) {
	var reqs []agent.Request
	in := scoreInput("", nil, &reqs)
	in.Findings = nil
	if res := Score(context.Background(), in); res.Calls != 0 || len(reqs) != 0 {
		t.Errorf("calls %d, want none", res.Calls)
	}
}
