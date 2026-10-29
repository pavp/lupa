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
	Class     Class  `json:"class"`
	Risk      Risk   `json:"risk"`
	Reason    string `json:"reason"`
	// Importers are the files outside this change that reach this one through imports, directly or not.
	Importers []string `json:"importers,omitempty"`
}

// Unresolved is an import in a changed file that names a repo file lupa could not find.
type Unresolved struct {
	From      string `json:"from"`
	Specifier string `json:"specifier"`
}

// Reach is what the import graph says about the change: importers outside it per changed file,
// and the imports of changed files that could not be resolved.
type Reach struct {
	Importers  map[string][]string
	Unresolved []Unresolved
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

	ReadingOrder []ReadingEntry `json:"reading_order"`
	Unresolved   []Unresolved   `json:"unresolved"`
}

// Build computes the signals of the change from base to head.
func Build(base, head string, files []File, reach Reach) Signals {
	s := Signals{Schema: Schema, Base: base, Head: head, Files: make([]File, len(files)), Unresolved: reach.Unresolved}
	if s.Unresolved == nil {
		s.Unresolved = []Unresolved{}
	}
	for i, f := range files {
		rule := Classify(f.Path)
		f.Class, f.Risk, f.Reason = rule.Class, rule.Risk, rule.Reason
		f.Importers = reach.Importers[f.Path]
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
	s.ReadingOrder = readingOrder(s.Files)
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
