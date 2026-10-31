package reach

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/pavp/lupa/internal/lang"
)

// reexportDepth bounds how many barrels a callee lookup follows.
const reexportDepth = 5

// Site is a line in a repo file.
type Site struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Callee is a declaration a changed symbol uses from another repo file; Line is 0 when the file was found
// but the declaration was not.
type Callee struct {
	Name string `json:"name"`
	Site
}

// Symbol is a top-level declaration the change touches, with the code that calls it and the code it calls.
type Symbol struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	Line    int      `json:"line"`
	End     int      `json:"end"`
	Callers []Site   `json:"callers,omitempty"`
	Callees []Callee `json:"callees,omitempty"`
}

// SymbolInput is what ChangedSymbols reads: the import graph, the head lines each changed file adds,
// and the language's symbols and resolver.
type SymbolInput struct {
	Graph    Graph
	Added    map[string][]int
	ReadFile func(string) ([]byte, error)
	Symbols  lang.Symbols
	Resolver lang.Resolver
}

// ChangedSymbols returns the declarations that contain an added line, sorted by file and line. Callers are
// the uses of the name elsewhere in its file and in importers whose binding resolves to the file or to a
// barrel whose re-exports carry its name to them, renaming ones included.
func ChangedSymbols(in SymbolInput) ([]Symbol, error) {
	s := scan{SymbolInput: in, src: map[string][]byte{}}
	var out []Symbol
	for _, file := range slices.Sorted(maps.Keys(in.Added)) {
		src, err := s.read(file)
		if err != nil {
			return nil, err
		}
		for _, d := range in.Symbols.Decls(src) {
			if !slices.ContainsFunc(in.Added[file], func(n int) bool { return n >= d.Line && n <= d.End }) {
				continue
			}
			sym := Symbol{File: file, Name: d.Name, Line: d.Line, End: d.End}
			if sym.Callers, err = s.callers(file, src, d); err != nil {
				return nil, err
			}
			if sym.Callees, err = s.callees(file, src, d); err != nil {
				return nil, err
			}
			out = append(out, sym)
		}
	}
	return out, nil
}

type scan struct {
	SymbolInput
	src map[string][]byte
}

func (s scan) read(file string) ([]byte, error) {
	if src, ok := s.src[file]; ok {
		return src, nil
	}
	src, err := s.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	s.src[file] = src
	return src, nil
}

func (s scan) callers(file string, src []byte, d lang.Decl) ([]Site, error) {
	var out []Site
	for _, n := range s.Symbols.Uses(src, d.Name) {
		if n < d.Line || n > d.End {
			out = append(out, Site{file, n})
		}
	}
	exported := d.Name
	if d.Default {
		exported = "default"
	}
	importers := s.Graph.Importers(file)
	carried, err := s.carriers(file, exported, importers)
	if err != nil {
		return nil, err
	}
	for _, imp := range importers {
		isrc, err := s.read(imp)
		if err != nil {
			return nil, err
		}
		for _, b := range s.Symbols.Bindings(isrc) {
			if b.Reexport {
				continue
			}
			if to, status := s.Resolver.Resolve(imp, b.Spec); status != lang.Resolved || !carried[export{to, b.Imported}] {
				continue
			}
			for _, n := range s.Symbols.Uses(isrc, b.Local) {
				out = append(out, Site{imp, n})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].File < out[j].File || out[i].File == out[j].File && out[i].Line < out[j].Line
	})
	return slices.Compact(out), nil
}

type export struct{ file, name string }

// carriers returns every module and name that exports the declaration: the file itself, then the barrels
// whose re-exports chain back to it. `export *` never carries a default export.
func (s scan) carriers(file, name string, importers []string) (map[export]bool, error) {
	carried := map[export]bool{{file, name}: true}
	for grew := true; grew; {
		grew = false
		for _, imp := range importers {
			isrc, err := s.read(imp)
			if err != nil {
				return nil, err
			}
			for _, b := range s.Symbols.Bindings(isrc) {
				if !b.Reexport {
					continue
				}
				to, status := s.Resolver.Resolve(imp, b.Spec)
				if status != lang.Resolved {
					continue
				}
				for e := range carried {
					next, ok := export{}, false
					switch {
					case e.file != to:
					case b.Imported == "*" && b.Local == "*" && e.name != "default":
						next, ok = export{imp, e.name}, true
					case b.Imported == e.name:
						next, ok = export{imp, b.Local}, true
					}
					if ok && !carried[next] {
						carried[next], grew = true, true
					}
				}
			}
		}
	}
	return carried, nil
}

func (s scan) callees(file string, src []byte, d lang.Decl) ([]Callee, error) {
	var out []Callee
	for _, b := range s.Symbols.Bindings(src) {
		if b.Reexport || b.Imported == "*" {
			continue
		}
		if !slices.ContainsFunc(s.Symbols.Uses(src, b.Local), func(n int) bool { return n >= d.Line && n <= d.End }) {
			continue
		}
		to, status := s.Resolver.Resolve(file, b.Spec)
		if status != lang.Resolved || to == file {
			continue
		}
		site, err := s.declaration(to, b.Imported, reexportDepth)
		if err != nil {
			return nil, err
		}
		out = append(out, Callee{Name: b.Local, Site: site})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// declaration finds where file declares the exported name, following re-exports through at most depth
// barrels; when nothing declares it, the site is file with Line 0.
func (s scan) declaration(file, name string, depth int) (Site, error) {
	src, err := s.read(file)
	if err != nil {
		return Site{}, err
	}
	for _, d := range s.Symbols.Decls(src) {
		if name == "default" && d.Default || name != "default" && !d.Default && d.Name == name {
			return Site{file, d.Line}, nil
		}
	}
	if depth > 0 {
		for _, b := range s.Symbols.Bindings(src) {
			if !b.Reexport || b.Local != name && b.Local != "*" {
				continue
			}
			next := b.Imported
			if next == "*" {
				next = name
			}
			if to, status := s.Resolver.Resolve(file, b.Spec); status == lang.Resolved && to != file {
				site, err := s.declaration(to, next, depth-1)
				if err != nil || site.Line > 0 {
					return site, err
				}
			}
		}
	}
	return Site{file, 0}, nil
}
