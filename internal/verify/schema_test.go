package verify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var group = []string{"src/a.ts", "src/b.ts"}

func validComment(file string) map[string]any {
	return map[string]any{
		"file":          file,
		"severity":      "severe",
		"claim":         "nil map write panics",
		"mechanism":     "an empty config reaches the write on line 3",
		"fix":           "initialize the map before writing",
		"quote":         "\tm[k] = v",
		"quote_settles": true,
		"harm":          "failure",
	}
}

func answer(t *testing.T, inspected []string, comments ...map[string]any) string {
	t.Helper()
	if comments == nil {
		comments = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"files_inspected": inspected, "comments": comments})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseRejectsUnusableAnswers(t *testing.T) {
	valid := answer(t, group)
	tests := []struct {
		name string
		text string
	}{
		{"not json", "looks fine to me"},
		{"prose before the object", "Here is my review:\n" + valid},
		{"prose after the object", valid + "\nThanks!"},
		{"two objects", valid + valid},
		{"an array", "[" + valid + "]"},
		{"missing files_inspected", `{"comments": []}`},
		{"missing comments", `{"files_inspected": ["src/a.ts", "src/b.ts"]}`},
		{"null comments", `{"files_inspected": ["src/a.ts", "src/b.ts"], "comments": null}`},
		{"two fences", "```json\n" + valid + "\n```\n```json\n" + valid + "\n```"},
		{"empty", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.text, group); err == nil {
				t.Fatalf("Parse(%q) error = nil, want an error", tt.text)
			}
		})
	}
}

func TestParseAcceptsValidAnswers(t *testing.T) {
	two := answer(t, group, validComment("src/a.ts"), validComment("src/b.ts"))
	want := Comment{
		File:         "src/a.ts",
		Severity:     "severe",
		Claim:        "nil map write panics",
		Mechanism:    "an empty config reaches the write on line 3",
		Fix:          "initialize the map before writing",
		Quote:        "\tm[k] = v",
		QuoteSettles: true,
		Harm:         "failure",
	}
	tests := []struct {
		name     string
		text     string
		comments int
	}{
		{"empty comments list", answer(t, group), 0},
		{"two comments", two, 2},
		{"json fence", "\n```json\n" + two + "\n```\n", 2},
		{"bare fence", "```\n" + two + "\n```", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.text, group)
			if err != nil {
				t.Fatalf("Parse error = %v", err)
			}
			if len(got.Comments) != tt.comments || len(got.Dropped) != 0 || len(got.Uninspected) != 0 || len(got.Notes) != 0 {
				t.Fatalf("Parse = %+v, want %d clean comments", got, tt.comments)
			}
			if tt.comments > 0 && got.Comments[0] != want {
				t.Errorf("Comments[0] = %+v, want %+v", got.Comments[0], want)
			}
		})
	}
	// An agent without an answer schema may leave harm out; the level then falls back to severity.
	noHarm := validComment("src/a.ts")
	delete(noHarm, "harm")
	if got, err := Parse(answer(t, group, noHarm), group); err != nil || len(got.Comments) != 1 || got.Comments[0].Harm != "" {
		t.Errorf("Parse without harm = %+v, %v; want the comment kept with no harm", got, err)
	}
}

func TestParseCoverage(t *testing.T) {
	text := answer(t, []string{"src/a.ts", "src/x.ts"}, validComment("src/b.ts"))
	got, err := Parse(text, []string{"src/c.ts", "src/a.ts", "src/b.ts"})
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if want := []string{"src/b.ts", "src/c.ts"}; !reflect.DeepEqual(got.Uninspected, want) {
		t.Errorf("Uninspected = %v, want %v", got.Uninspected, want)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "src/x.ts") {
		t.Errorf("Notes = %v, want one note naming src/x.ts", got.Notes)
	}
	if len(got.Comments) != 1 || len(got.Dropped) != 0 {
		t.Errorf("Comments = %v, Dropped = %v, want the comment kept", got.Comments, got.Dropped)
	}
}

func TestParseDropsBadCommentsOnTheirOwn(t *testing.T) {
	with := func(key string, value any) map[string]any {
		c := validComment("src/b.ts")
		c[key] = value
		return c
	}
	without := func(key string) map[string]any {
		c := validComment("src/b.ts")
		delete(c, key)
		return c
	}
	tests := []struct {
		name   string
		bad    map[string]any
		reason string
	}{
		{"file outside the group", with("file", "src/z.ts"), "file"},
		{"bad severity", with("severity", "critical"), "severity"},
		{"bad harm", with("harm", "catastrophic"), "harm"},
		{"empty claim", with("claim", "  "), "claim"},
		{"empty mechanism", with("mechanism", ""), "mechanism"},
		{"empty fix", with("fix", "\n"), "fix"},
		{"empty quote", with("quote", " "), "quote"},
		{"missing claim", without("claim"), "claim"},
		{"placeholder n/a", with("claim", "N/A"), "claim"},
		{"placeholder na", with("fix", "na"), "fix"},
		{"placeholder none", with("mechanism", " None "), "mechanism"},
		{"placeholder todo", with("fix", "TODO"), "fix"},
		{"placeholder tbd", with("mechanism", "tbd"), "mechanism"},
		{"placeholder tba", with("claim", "TBA"), "claim"},
		{"placeholder dots", with("quote", "..."), "quote"},
		{"placeholder ellipsis", with("quote", "…"), "quote"},
		{"placeholder word", with("claim", "Placeholder"), "claim"},
		{"placeholder dash", with("fix", "-"), "fix"},
		{"placeholder template", with("claim", "<what is wrong>"), "claim"},
		{"placeholder word in quote", with("quote", "TODO"), "quote"},
		{"missing quote_settles", without("quote_settles"), "quote_settles"},
		{"non-boolean quote_settles", with("quote_settles", "yes"), "quote_settles"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, last := validComment("src/a.ts"), validComment("src/b.ts")
			last["claim"] = "second finding"
			last["extra"] = "ignored"
			got, err := Parse(answer(t, group, first, tt.bad, last), group)
			if err != nil {
				t.Fatalf("Parse error = %v", err)
			}
			if len(got.Dropped) != 1 || got.Dropped[0].Index != 1 {
				t.Fatalf("Dropped = %+v, want only index 1", got.Dropped)
			}
			if !strings.Contains(got.Dropped[0].Reason, tt.reason) {
				t.Errorf("Reason = %q, want it to name %q", got.Dropped[0].Reason, tt.reason)
			}
			if f, _ := tt.bad["file"].(string); got.Dropped[0].File != f {
				t.Errorf("Dropped File = %q, want %q", got.Dropped[0].File, f)
			}
			if len(got.Comments) != 2 || got.Comments[0].File != "src/a.ts" || got.Comments[1].Claim != "second finding" {
				t.Errorf("Comments = %+v, want first and last in order", got.Comments)
			}
		})
	}
}

func TestParseKeepsRealTextThatResemblesPlaceholders(t *testing.T) {
	c := validComment("src/a.ts")
	c["quote"] = "if a < b {"
	c["claim"] = "returns none of the items when n/a is passed"
	got, err := Parse(answer(t, group, c), group)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if len(got.Comments) != 1 {
		t.Errorf("Dropped = %+v, want the comment kept", got.Dropped)
	}
}

func TestParseKeepsAJSXTagAsAQuote(t *testing.T) {
	c := validComment("src/a.ts")
	c["quote"] = `<Badge label="new">`
	got, err := Parse(answer(t, group, c), group)
	if err != nil {
		t.Fatalf("Parse error = %v", err)
	}
	if len(got.Comments) != 1 {
		t.Errorf("Dropped = %+v, want a JSX line kept: the quote is code, not a template", got.Dropped)
	}
}
