// Package reach answers "what does this change reach": the files that import a changed file, directly or transitively.
package reach

import "sort"

// Unresolved is an import specifier that names a file inside the repo but could not be resolved.
type Unresolved struct {
	From      string `json:"from"`
	Specifier string `json:"specifier"`
	Line      int    `json:"line"`
}

// Graph is a file-level import graph. Paths are repo-relative with forward slashes.
type Graph struct {
	Imports    map[string][]string `json:"imports"`
	Unresolved []Unresolved        `json:"unresolved"`
	// Ignored are imports of paths the repo's .gitignore covers, kept apart from Unresolved.
	Ignored []Unresolved `json:"ignored,omitempty"`
}

// DirectImporters returns the files that import file themselves, sorted.
func (g Graph) DirectImporters(file string) []string {
	var out []string
	for from, tos := range g.Imports {
		for _, to := range tos {
			if to == file && from != file {
				out = append(out, from)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Importers returns every file that reaches file through one or more imports, sorted.
func (g Graph) Importers(file string) []string {
	reverse := map[string][]string{}
	for from, tos := range g.Imports {
		for _, to := range tos {
			reverse[to] = append(reverse[to], from)
		}
	}
	seen := map[string]bool{}
	queue := []string{file}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, from := range reverse[cur] {
			if !seen[from] && from != file {
				seen[from] = true
				queue = append(queue, from)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
