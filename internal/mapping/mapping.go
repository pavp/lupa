// Package mapping turns a diff into the map's signals. It is pure: data in, data out.
package mapping

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
}

// Build computes the signals of the change from base to head.
func Build(base, head string, files []File) Signals {
	s := Signals{Schema: Schema, Base: base, Head: head, Files: make([]File, len(files))}
	for i, f := range files {
		rule := Classify(f.Path)
		f.Class, f.Risk, f.Reason = rule.Class, rule.Risk, rule.Reason
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
	return s
}
