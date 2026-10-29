package reach

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/pavp/lupa/internal/lang"
)

// Build scans every file the provider owns and resolves its imports into a file-level graph.
// Imports of packages outside the repo are dropped; imports naming a missing repo file are kept as Unresolved.
func Build(files []string, readFile func(string) ([]byte, error), p lang.Provider) (Graph, error) {
	resolver, err := p.NewResolver(files, readFile)
	if err != nil {
		return Graph{}, err
	}
	g := Graph{Imports: map[string][]string{}}
	for _, f := range files {
		if !p.Owns(f) {
			continue
		}
		src, err := readFile(f)
		if err != nil {
			return Graph{}, fmt.Errorf("%s: %w", f, err)
		}
		targets := map[string]bool{}
		for _, spec := range p.Imports(src) {
			switch to, status := resolver.Resolve(f, spec); status {
			case lang.Resolved:
				if to != f {
					targets[to] = true
				}
			case lang.Unresolved:
				g.Unresolved = append(g.Unresolved, Unresolved{From: f, Specifier: spec, Line: lineOf(src, spec)})
			}
		}
		imports := make([]string, 0, len(targets))
		for to := range targets {
			imports = append(imports, to)
		}
		sort.Strings(imports)
		g.Imports[f] = imports
	}
	sort.Slice(g.Unresolved, func(i, j int) bool {
		a, b := g.Unresolved[i], g.Unresolved[j]
		return a.From < b.From || a.From == b.From && a.Specifier < b.Specifier
	})
	return g, nil
}

// lineOf returns the 1-based line where spec first appears quoted in src, or 0.
func lineOf(src []byte, spec string) int {
	for i, line := range bytes.Split(src, []byte("\n")) {
		for _, q := range []string{"'", "\"", "`"} {
			if bytes.Contains(line, []byte(q+spec+q)) {
				return i + 1
			}
		}
	}
	return 0
}
