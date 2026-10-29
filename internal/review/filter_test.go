package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/verify"
)

const rubric = "severe only when the change causes it, realistic input reaches it, and it names an observable harm"

func leveled(level, claim string) Anchored {
	return Anchored{Finding: verify.Finding{
		Comment: verify.Comment{
			File: "src/cart.ts", Severity: "severe", Claim: claim,
			Mechanism: "a null list reaches the loop", Quote: realQuote,
		},
		Location: verify.Location{File: "src/cart.ts", Side: "new", Start: 11, End: 11},
		Level:    level,
	}}
}

func filterInput(fake *agent.Fake, findings ...Anchored) FilterInput {
	return FilterInput{
		Agent:    fake,
		Dir:      "/tmp/worktree",
		Timeout:  2 * time.Minute,
		Findings: findings,
	}
}

func claims(fs []Anchored) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Finding.Comment.Claim)
	}
	return out
}

func TestFilterSkipsFindingsThatAreNotImportant(t *testing.T) {
	fake := replying(`{"results": []}`)
	in := filterInput(fake,
		leveled(verify.LevelBlocking, "blocking"),
		leveled(verify.LevelNote, "note"),
		leveled(verify.LevelFollowUp, "follow-up"),
	)
	res := Filter(context.Background(), in)

	if fake.Calls() != 0 || res.Calls != 0 {
		t.Fatalf("calls = %d, Calls = %d, want 0 and 0", fake.Calls(), res.Calls)
	}
	if got := strings.Join(claims(res.Kept), ","); got != "blocking,note,follow-up" {
		t.Errorf("kept = %q, want every finding in input order", got)
	}
	if len(res.Refuted) != 0 || len(res.Outcomes) != 0 {
		t.Errorf("refuted = %v, outcomes = %v, want none", res.Refuted, res.Outcomes)
	}
}

func TestFilterSendsOnlyImportantFindingsInOneCall(t *testing.T) {
	var req agent.Request
	fake := &agent.Fake{Reply: func(r agent.Request) (string, error) {
		req = r
		return `{"results": [{"id": 1, "outcome": "corroborated"}, {"id": 3, "outcome": "inconclusive"}]}`, nil
	}}
	in := filterInput(fake,
		leveled(verify.LevelBlocking, "blocking claim"),
		leveled(verify.LevelImportant, "first important claim"),
		leveled(verify.LevelNote, "note claim"),
		leveled(verify.LevelImportant, "second important claim"),
	)
	res := Filter(context.Background(), in)

	if fake.Calls() != 1 || res.Calls != 1 {
		t.Fatalf("calls = %d, Calls = %d, want 1 and 1", fake.Calls(), res.Calls)
	}
	if req.Dir != "/tmp/worktree" || req.Timeout != 2*time.Minute {
		t.Errorf("request dir, timeout = %q, %v, want the run's", req.Dir, req.Timeout)
	}
	if !strings.Contains(req.SystemPrompt, rubric) {
		t.Error("system prompt lacks the severity rubric")
	}
	for _, c := range []string{"blocking claim", "note claim"} {
		if strings.Contains(req.Prompt, c) {
			t.Errorf("prompt carries %q, which must skip the refuter", c)
		}
	}
	for _, c := range []string{"first important claim", "second important claim"} {
		open := strings.Index(req.Prompt, "<<<LUPA-DATA finding ")
		if open < 0 {
			t.Fatal("prompt has no finding data block")
		}
		at := strings.Index(req.Prompt, c)
		closing := strings.Index(req.Prompt[max(at, 0):], "LUPA-DATA finding ")
		if at < open || closing < 0 {
			t.Errorf("claim %q is not inside a fenced finding block", c)
		}
	}
	if got := strings.Join(claims(res.Kept), ","); got != "blocking claim,first important claim,note claim,second important claim" {
		t.Errorf("kept = %q, want every finding in input order", got)
	}
	want := map[int]string{1: OutcomeCorroborated, 3: OutcomeInconclusive}
	if fmt.Sprint(res.Outcomes) != fmt.Sprint(want) {
		t.Errorf("outcomes = %v, want %v", res.Outcomes, want)
	}
}

func TestFilterDoesNotApplyARefutationYet(t *testing.T) {
	fake := replying("```json\n" + `{"results": [{"id": 1, "outcome": "refuted", "citation": {"file": "src/cart.ts", "line": 6, "quote": "if (!items) {"}}]}` + "\n```")
	res := Filter(context.Background(), filterInput(fake,
		leveled(verify.LevelImportant, "first claim"),
		leveled(verify.LevelImportant, "refuted claim"),
	))

	if got := strings.Join(claims(res.Kept), ","); got != "first claim,refuted claim" {
		t.Errorf("kept = %q, want both findings", got)
	}
	if len(res.Refuted) != 0 {
		t.Errorf("refuted = %v, want none", res.Refuted)
	}
	if res.Outcomes[1] != OutcomeInconclusive || res.Outcomes[0] != OutcomeInconclusive {
		t.Errorf("outcomes = %v, want both inconclusive", res.Outcomes)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "refutation not applied: citations are not verified yet") {
		t.Errorf("notes = %q, want the refutation-not-applied note", res.Notes)
	}
}

func TestFilterKeepsEverythingWhenTheRunFails(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		err   error
		note  string
	}{
		{"agent error", "", errors.New("timed out after 2m0s"), "filter run failed: timed out after 2m0s"},
		{"prose reply", "All claims hold.", nil, "filter reply malformed"},
		{"data after the object", `{"results": []} {"results": []}`, nil, "filter reply malformed"},
		{"no results key", `{"outcome": "refuted"}`, nil, "filter reply malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &agent.Fake{Reply: func(agent.Request) (string, error) { return tt.reply, tt.err }}
			res := Filter(context.Background(), filterInput(fake,
				leveled(verify.LevelImportant, "first"),
				leveled(verify.LevelBlocking, "second"),
				leveled(verify.LevelImportant, "third"),
			))

			if got := strings.Join(claims(res.Kept), ","); got != "first,second,third" {
				t.Errorf("kept = %q, want every finding in input order", got)
			}
			if len(res.Refuted) != 0 {
				t.Errorf("refuted = %v, want none", res.Refuted)
			}
			if !strings.HasPrefix(res.Note, tt.note) {
				t.Errorf("note = %q, want prefix %q", res.Note, tt.note)
			}
			if res.Calls != 1 {
				t.Errorf("Calls = %d, want 1", res.Calls)
			}
		})
	}
}

func TestFilterIgnoresUnknownAndDuplicateIDs(t *testing.T) {
	fake := replying(`{"results": [
		{"id": 0, "outcome": "corroborated"},
		{"id": 0, "outcome": "refuted", "citation": {"file": "src/cart.ts", "line": 6, "quote": "if (!items) {"}},
		{"id": 1, "outcome": "refuted", "citation": {"file": "src/cart.ts", "line": 6, "quote": "if (!items) {"}},
		{"id": 9, "outcome": "refuted", "citation": {"file": "src/cart.ts", "line": 6, "quote": "if (!items) {"}}
	]}`)
	res := Filter(context.Background(), filterInput(fake,
		leveled(verify.LevelImportant, "important"),
		leveled(verify.LevelBlocking, "blocking"),
	))

	if got := strings.Join(claims(res.Kept), ","); got != "important,blocking" {
		t.Errorf("kept = %q, want both findings", got)
	}
	if len(res.Refuted) != 0 {
		t.Errorf("refuted = %v, want none", res.Refuted)
	}
	if res.Outcomes[0] != OutcomeCorroborated || len(res.Outcomes) != 1 {
		t.Errorf("outcomes = %v, want only 0 corroborated", res.Outcomes)
	}
	notes := strings.Join(res.Notes, "\n")
	for _, want := range []string{"duplicate result for id 0", "unknown id 1", "unknown id 9"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes = %q, want one containing %q", res.Notes, want)
		}
	}
}
