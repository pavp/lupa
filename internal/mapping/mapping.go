// Package mapping turns a diff into the map's signals. It is pure: data in, data out.
package mapping

import "fmt"

// Schema versions the signals JSON; consumers check it before reading anything else.
const Schema = "lupa.signals/v0"

// File is one changed file. Binary files have no line counts.
type File struct {
	Path      string `json:"path"`
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
// and the imports of changed files that could not be resolved.
type Reach struct {
	Importers  map[string][]string
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

	ReviewNow Gate `json:"review_now"`
	// SinceLastLook is null on the first map of a target.
	SinceLastLook *LastLook      `json:"since_last_look"`
	ReadingOrder  []ReadingEntry `json:"reading_order"`
	Unresolved    []Unresolved   `json:"unresolved"`

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

// Gate is the review-now decision and why.
type Gate struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`
}

// SizeBudget is the most changed lines, generated files excluded, a change can have and still be reviewed in one sitting.
const SizeBudget = 400

// Input is everything Build needs: the range, the changed files, what reaches them, and their owners.
type Input struct {
	Base   string
	Head   string
	Files  []File
	Reach  Reach
	Owners map[string][]string
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
	s.ReviewNow = Gate{Review, fmt.Sprintf("%d changed lines, generated excluded, within %d", s.Size.Changed, SizeBudget)}
	if s.Size.Changed > SizeBudget {
		s.ReviewNow = Gate{HandBack, fmt.Sprintf("%d changed lines, generated excluded, over %d: suggest a split", s.Size.Changed, SizeBudget)}
	}
	s.ReadingOrder, s.TestsNotImportingSubject = readingOrder(s.Files, reach.Imports)
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
