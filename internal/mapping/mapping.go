// Package mapping turns a diff into the map's signals. It is pure: data in, data out.
package mapping

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Schema versions the signals JSON; consumers check it before reading anything else.
const Schema = "lupa.signals/v0"

// File is one changed file. Binary files have no line counts.
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	// FirstLine is the first changed line on the head side, 0 when the file was deleted.
	FirstLine int    `json:"first_line,omitempty"`
	Class     Class  `json:"class"`
	Risk      Risk   `json:"risk"`
	Reason    string `json:"reason"`
	// Importers are the files outside this change that reach this one through imports, directly or not.
	Importers []string `json:"importers,omitempty"`
	// DirectImporters are the Importers that import this file themselves.
	DirectImporters []string `json:"direct_importers,omitempty"`
	// Owners come from the base branch's CODEOWNERS, so a change cannot pick its own reviewers.
	Owners []string `json:"owners,omitempty"`
}

// Unresolved is an import in a changed file that names a repo file lupa could not find.
type Unresolved struct {
	From      string `json:"from"`
	Specifier string `json:"specifier"`
	Line      int    `json:"line"`
}

// Reach is what the import graph says about the change: importers outside it per changed file,
// those of them that import it directly, and the imports of changed files that could not be resolved.
type Reach struct {
	Importers  map[string][]string
	Direct     map[string][]string
	Unresolved []Unresolved
	// Imports are the resolved direct imports of changed files; nil when the language is not analyzed.
	Imports map[string][]string
}

// TestMismatch is a test named after a changed file that does not import it, so it may not exercise it.
type TestMismatch struct {
	Test    string `json:"test"`
	Subject string `json:"subject"`
	Line    int    `json:"line"`
}

// Size totals the authored change; generated files are counted apart and add no lines.
type Size struct {
	Files          int `json:"files"`
	Additions      int `json:"additions"`
	Deletions      int `json:"deletions"`
	Changed        int `json:"changed"`
	GeneratedFiles int `json:"generated_files"`
}

// Signals is the map of one change, the shape written by `lupa map --json`.
type Signals struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Files  []File `json:"files"`
	Size   Size   `json:"size"`

	// PR is null for a branch range.
	PR        *PRInfo `json:"pr"`
	ReviewNow Gate    `json:"review_now"`
	// SinceLastLook is null on the first map of a target.
	SinceLastLook *LastLook      `json:"since_last_look"`
	ReadingOrder  []ReadingEntry `json:"reading_order"`
	// Claims is null for a branch range.
	Claims     []Claim      `json:"claims"`
	Unresolved []Unresolved `json:"unresolved"`

	TestsNotImportingSubject []TestMismatch `json:"tests_not_importing_subject"`
}

// LastLook is what changed since the last map of the same target.
type LastLook struct {
	Since string   `json:"since"`
	Files []string `json:"files"`
}

// Decision is whether to review the change now or hand it back to its author.
type Decision string

const (
	Review   Decision = "review"
	HandBack Decision = "hand-back"
)

// Gate is the review-now decision, why, and on a hand-back the reply to suggest to the author.
type Gate struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`
	Reply    string   `json:"reply,omitempty"`
}

// PR is what the forge says about a change that is a pull request: its description, linked issues,
// and the open pull requests of its repo, itself included or not.
type PR struct {
	Number           int
	URL, Title       string
	BaseRef, HeadRef string
	Body             string
	LinkedIssues     int
	Open             []OpenPR
}

// OpenPR is another open pull request and the files it changes.
type OpenPR struct {
	Number           int
	Title            string
	BaseRef, HeadRef string
	Files            []string
}

// PRInfo names the pull request in the signals.
type PRInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// purposeWords is the fewest words of description that state a purpose without a linked issue.
const purposeWords = 10

// SizeBudget is the most changed lines, generated files excluded, a change can have and still be reviewed in one sitting.
const SizeBudget = 400

// Input is everything Build needs: the range, the changed files, what reaches them, and their owners.
type Input struct {
	Base   string
	Head   string
	Files  []File
	Reach  Reach
	Owners map[string][]string
	// PR is nil for a branch range.
	PR *PR
	// Patches are the unified diffs per path; claims are looked up in them.
	Patches map[string]string
}

// Build computes the signals of the change.
func Build(in Input) Signals {
	files, reach := in.Files, in.Reach
	s := Signals{Schema: Schema, Base: in.Base, Head: in.Head, Files: make([]File, len(files)), Unresolved: reach.Unresolved}
	if s.Unresolved == nil {
		s.Unresolved = []Unresolved{}
	}
	for i, f := range files {
		rule := Classify(f.Path)
		f.Class, f.Risk, f.Reason = rule.Class, rule.Risk, rule.Reason
		f.Importers = reach.Importers[f.Path]
		f.DirectImporters = reach.Direct[f.Path]
		f.Owners = in.Owners[f.Path]
		if len(f.Importers) > 0 && f.Class == Skim {
			f.Class, f.Risk = ReadFirst, Impact
			f.Reason = fmt.Sprintf("imported by %d %s outside this change", len(f.Importers), plural(len(f.Importers), "file", "files"))
		}
		s.Files[i] = f
		if f.Class == Skip {
			s.Size.GeneratedFiles++
			continue
		}
		s.Size.Files++
		s.Size.Additions += f.Additions
		s.Size.Deletions += f.Deletions
	}
	s.Size.Changed = s.Size.Additions + s.Size.Deletions
	if in.PR != nil {
		s.PR = &PRInfo{Number: in.PR.Number, URL: in.PR.URL, Title: in.PR.Title}
		if s.Claims = claims(in.PR.Body, s.Files, in.Patches); s.Claims == nil {
			s.Claims = []Claim{}
		}
	}
	s.ReviewNow = gate(s, in.PR)
	s.ReadingOrder, s.TestsNotImportingSubject = readingOrder(s.Files, reach.Imports)
	return s
}

// gate decides review now or hand back; the first rule that matches wins: a possible duplicate,
// the size, then a missing purpose. The PR rules apply only to a pull request.
func gate(s Signals, pr *PR) Gate {
	if pr != nil {
		if g, ok := duplicate(s.Files, pr); ok {
			return g
		}
	}
	if s.Size.Changed > SizeBudget {
		return Gate{HandBack, fmt.Sprintf("%d changed lines, generated excluded, over %d: suggest a split", s.Size.Changed, SizeBudget),
			fmt.Sprintf("This change has %d changed lines without generated files. Could you split it into smaller PRs?", s.Size.Changed)}
	}
	if pr != nil {
		reply := "Could you say in the description what this change is for, or link its issue?"
		switch words := purposeWordCount(pr.Body); {
		case words == 0:
			return Gate{HandBack, "no description: ask for intent", reply}
		case words < purposeWords && pr.LinkedIssues == 0:
			return Gate{HandBack, "no linked issue and no stated purpose: ask for intent", reply}
		}
	}
	return Gate{Decision: Review, Reason: fmt.Sprintf("%d changed lines, generated excluded, within %d", s.Size.Changed, SizeBudget)}
}

// duplicate finds the first other open PR that changes at least half of this change's files,
// generated files excluded. A PR stacked on this one, or under it, shares its files by design.
func duplicate(files []File, pr *PR) (Gate, bool) {
	var own []string
	for _, f := range files {
		if f.Class != Skip {
			own = append(own, f.Path)
		}
	}
	for _, o := range pr.Open {
		if o.Number == pr.Number || o.HeadRef != "" && o.HeadRef == pr.BaseRef || o.BaseRef != "" && o.BaseRef == pr.HeadRef {
			continue
		}
		shared := 0
		for _, p := range own {
			if slices.Contains(o.Files, p) {
				shared++
			}
		}
		if shared > 0 && 2*shared >= len(own) {
			name := fmt.Sprintf("PR #%d", o.Number)
			if o.Title != "" {
				name += " (" + o.Title + ")"
			}
			return Gate{HandBack, fmt.Sprintf("%s changes %d of the same %d files: possible duplicate", name, shared, len(own)),
				fmt.Sprintf("PR #%d changes the same files. Is this one a duplicate, or how do the two relate?", o.Number)}, true
		}
	}
	return Gate{}, false
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	boilerplate = regexp.MustCompile(`^\s*(#|[-*]\s*\[[ xX]\]|$)`)
)

// purposeWordCount counts the description's words outside HTML comments, headings and checkboxes,
// which PR templates leave behind when nobody writes a description.
func purposeWordCount(body string) int {
	n := 0
	for _, line := range strings.Split(htmlComment.ReplaceAllString(body, ""), "\n") {
		if !boilerplate.MatchString(line) {
			n += len(strings.Fields(line))
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
