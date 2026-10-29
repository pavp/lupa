// Package verify checks a reviewer's answer with code before any of it reaches the report.
package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Comment is one reviewer comment that passed the schema checks.
type Comment struct {
	File         string
	Severity     string
	Claim        string
	Mechanism    string
	Fix          string
	Quote        string
	QuoteSettles bool
}

// Dropped is a comment rejected on its own, by its index in the answer.
type Dropped struct {
	Index  int
	File   string
	Reason string
}

// Result is a parsed answer: surviving comments, dropped ones, and coverage gaps.
type Result struct {
	Comments    []Comment
	Dropped     []Dropped
	Uninspected []string
	Notes       []string
}

type rawComment struct {
	File         string `json:"file"`
	Severity     string `json:"severity"`
	Claim        string `json:"claim"`
	Mechanism    string `json:"mechanism"`
	Fix          string `json:"fix"`
	Quote        string `json:"quote"`
	QuoteSettles *bool  `json:"quote_settles"`
}

var placeholders = []string{"n/a", "na", "none", "todo", "tbd", "tba", "...", "…", "placeholder", "-"}

// Parse validates a reviewer's final answer for group. An error means the whole
// answer is unusable; a bad comment is dropped on its own into Result.Dropped.
func Parse(text string, group []string) (Result, error) {
	var answer struct {
		FilesInspected []string          `json:"files_inspected"`
		Comments       []json.RawMessage `json:"comments"`
	}
	dec := json.NewDecoder(strings.NewReader(unfence(text)))
	if err := dec.Decode(&answer); err != nil {
		return Result{}, fmt.Errorf("answer is not one JSON object: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Result{}, errors.New("answer has data after the JSON object")
	}
	if answer.FilesInspected == nil {
		return Result{}, errors.New("answer has no files_inspected list")
	}
	if answer.Comments == nil {
		return Result{}, errors.New("answer has no comments list")
	}

	var res Result
	for _, p := range group {
		if !slices.Contains(answer.FilesInspected, p) {
			res.Uninspected = append(res.Uninspected, p)
		}
	}
	slices.Sort(res.Uninspected)
	for _, p := range answer.FilesInspected {
		if !slices.Contains(group, p) {
			res.Notes = append(res.Notes, fmt.Sprintf("files_inspected lists %s, which is not in the group", p))
		}
	}

	for i, raw := range answer.Comments {
		var c rawComment
		if err := json.Unmarshal(raw, &c); err != nil {
			res.Dropped = append(res.Dropped, Dropped{Index: i, File: c.File, Reason: "malformed comment: " + err.Error()})
			continue
		}
		if reason := check(c, group); reason != "" {
			res.Dropped = append(res.Dropped, Dropped{Index: i, File: c.File, Reason: reason})
			continue
		}
		res.Comments = append(res.Comments, Comment{
			File: c.File, Severity: c.Severity, Claim: c.Claim, Mechanism: c.Mechanism,
			Fix: c.Fix, Quote: c.Quote, QuoteSettles: *c.QuoteSettles,
		})
	}
	return res, nil
}

// unfence strips one surrounding ```json or ``` fence, if the whole text is fenced.
func unfence(text string) string {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") {
		return t
	}
	first, body, ok := strings.Cut(t, "\n")
	if !ok || (first != "```" && first != "```json") {
		return t
	}
	return strings.TrimSuffix(body, "```")
}

func check(c rawComment, group []string) string {
	if !slices.Contains(group, c.File) {
		return fmt.Sprintf("file outside the group: %q", c.File)
	}
	if c.Severity != "severe" && c.Severity != "warning" {
		return fmt.Sprintf("severity must be severe or warning, got %q", c.Severity)
	}
	for _, f := range []struct{ name, value string }{
		{"claim", c.Claim}, {"mechanism", c.Mechanism}, {"fix", c.Fix}, {"quote", c.Quote},
	} {
		v := strings.ToLower(strings.TrimSpace(f.value))
		switch {
		case v == "":
			return f.name + " is empty"
		case slices.Contains(placeholders, v) || f.name != "quote" && isTemplate(v):
			return f.name + " is placeholder text"
		}
	}
	if c.QuoteSettles == nil {
		return "quote_settles is missing"
	}
	return ""
}

// isTemplate reports a value that is a single angle-bracketed template, like <what is wrong>.
// It is not applied to quotes: a quote is code, and a lone JSX tag looks the same.
func isTemplate(v string) bool {
	return len(v) > 2 && v[0] == '<' && v[len(v)-1] == '>' && !strings.ContainsAny(v[1:len(v)-1], "<>")
}
