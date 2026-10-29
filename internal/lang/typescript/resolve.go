package typescript

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/pavp/lupa/internal/lang"
)

// Status aliases lang.Status so the provider and the core share one set of outcomes.
type Status = lang.Status

const (
	Resolved   = lang.Resolved
	External   = lang.External
	Unresolved = lang.Unresolved
)

var codeExts = []string{".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs"}

// jsToTS maps an emitted extension to the sources the compiler accepts for it.
var jsToTS = map[string][]string{".js": {".ts", ".tsx"}, ".jsx": {".tsx"}, ".mjs": {".mts"}, ".cjs": {".cts"}}

type alias struct {
	prefix, suffix string
	wildcard       bool
	targets        []string
}

type config struct {
	baseURL string
	aliases []alias
}

// Resolver maps import specifiers to repo files the way the TypeScript compiler would, approximately:
// relative paths, extension mapping, directory index files, and `paths`/`baseUrl` from the nearest
// tsconfig.json or jsconfig.json, following relative `extends`.
type Resolver struct {
	files      map[string]bool
	configs    map[string]*config
	workspaces []workspacePkg
}

// NewResolver builds a resolver over the repo's files; readFile reads a repo-relative file.
// A tsconfig.json or jsconfig.json that does not parse is an error, never silently skipped.
func NewResolver(files []string, readFile func(path string) ([]byte, error)) (*Resolver, error) {
	r := &Resolver{files: make(map[string]bool, len(files)), configs: map[string]*config{}}
	for _, f := range files {
		r.files[f] = true
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	// tsconfig.json first: the compiler prefers it over a jsconfig.json in the same directory.
	for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
		if err := r.loadConfigs(sorted, name, readFile); err != nil {
			return nil, err
		}
	}
	var err error
	if r.workspaces, err = loadWorkspaces(sorted, readFile); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Resolver) loadConfigs(files []string, name string, readFile func(string) ([]byte, error)) error {
	for _, f := range files {
		if path.Base(f) != name {
			continue
		}
		if _, done := r.configs[path.Dir(f)]; done {
			continue
		}
		c, err := loadConfig(f, readFile, 0)
		if err != nil {
			return err
		}
		r.configs[path.Dir(f)] = c.final()
	}
	return nil
}

// Resolve returns the repo file that spec, imported from the file from, names.
func (r *Resolver) Resolve(from, spec string) (string, Status) {
	if spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		return r.found(r.file(path.Join(path.Dir(from), spec)))
	}
	if c := r.configFor(from); c != nil {
		if a, ok := match(c.aliases, spec); ok {
			star := strings.TrimSuffix(strings.TrimPrefix(spec, a.prefix), a.suffix)
			for _, t := range a.targets {
				if f := r.file(strings.Replace(t, "*", star, 1)); f != "" {
					return f, Resolved
				}
			}
			return "", Unresolved
		}
	}
	if f, status, ok := r.resolveWorkspace(spec); ok {
		return f, status
	}
	if c := r.configFor(from); c != nil {
		if c.baseURL != "" {
			if f := r.file(path.Join(c.baseURL, spec)); f != "" {
				return f, Resolved
			}
		}
	}
	if strings.HasPrefix(spec, "/") {
		return "", Unresolved
	}
	return "", External
}

func (r *Resolver) found(f string) (string, Status) {
	if f == "" {
		return "", Unresolved
	}
	return f, Resolved
}

// file returns the source file p names, trying the compiler's extension mapping, then added
// extensions, then a directory index. Declaration files are types, not code, and never match.
func (r *Resolver) file(p string) string {
	p = path.Clean(p)
	ok := func(f string) bool { return r.files[f] && !strings.HasSuffix(f, ".d.ts") }
	ext := path.Ext(p)
	if ok(p) && ext != "" {
		return p
	}
	for _, alt := range jsToTS[ext] {
		if f := strings.TrimSuffix(p, ext) + alt; ok(f) {
			return f
		}
	}
	for _, e := range codeExts {
		if ok(p + e) {
			return p + e
		}
	}
	for _, e := range codeExts {
		if f := path.Join(p, "index"+e); ok(f) {
			return f
		}
	}
	return ""
}

func (r *Resolver) configFor(from string) *config {
	for dir := path.Dir(from); ; dir = path.Dir(dir) {
		if c, ok := r.configs[dir]; ok {
			return c
		}
		if dir == "." || dir == "/" {
			return nil
		}
	}
}

// match picks the alias with the longest matching prefix, as the compiler does.
func match(aliases []alias, spec string) (alias, bool) {
	var best alias
	found := false
	for _, a := range aliases {
		hit := a.wildcard && strings.HasPrefix(spec, a.prefix) && strings.HasSuffix(spec, a.suffix) &&
			len(spec) >= len(a.prefix)+len(a.suffix)
		if !a.wildcard {
			hit = spec == a.prefix
		}
		if hit && (!found || len(a.prefix) > len(best.prefix)) {
			best, found = a, true
		}
	}
	return best, found
}

// loaded is one config file merged with what it extends; paths stay relative to their base.
type loaded struct {
	baseURL  string
	hasBase  bool
	paths    map[string][]string
	pathsDir string
}

func (l loaded) final() *config {
	base := l.pathsDir
	if l.hasBase {
		base = l.baseURL
	}
	c := &config{}
	if l.hasBase {
		c.baseURL = l.baseURL
	}
	for pattern, targets := range l.paths {
		a := alias{prefix: pattern}
		if i := strings.Index(pattern, "*"); i >= 0 {
			a = alias{prefix: pattern[:i], suffix: pattern[i+1:], wildcard: true}
		}
		for _, t := range targets {
			a.targets = append(a.targets, path.Join(base, t))
		}
		c.aliases = append(c.aliases, a)
	}
	sort.Slice(c.aliases, func(i, j int) bool { return c.aliases[i].prefix < c.aliases[j].prefix })
	return c
}

func loadConfig(file string, readFile func(string) ([]byte, error), depth int) (loaded, error) {
	if depth > 10 {
		return loaded{}, fmt.Errorf("%s: extends chain too deep", file)
	}
	raw, err := readFile(file)
	if err != nil {
		return loaded{}, fmt.Errorf("%s: %w", file, err)
	}
	var doc struct {
		Extends         json.RawMessage `json:"extends"`
		CompilerOptions struct {
			BaseURL *string             `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(stripJSONC(raw), &doc); err != nil {
		return loaded{}, fmt.Errorf("%s: %w", file, err)
	}
	// `extends` is one config or, since TypeScript 5.0, a list applied in order, each overriding the ones before.
	var parents []string
	if len(doc.Extends) > 0 && json.Unmarshal(doc.Extends, &parents) != nil {
		var one string
		if err := json.Unmarshal(doc.Extends, &one); err != nil {
			return loaded{}, fmt.Errorf("%s: extends: %w", file, err)
		}
		parents = []string{one}
	}
	dir := path.Dir(file)
	var l loaded
	for _, ext := range parents {
		// A package `extends` lives in node_modules, which lupa does not read; only relative ones are followed.
		if !strings.HasPrefix(ext, "./") && !strings.HasPrefix(ext, "../") {
			continue
		}
		parent := path.Join(dir, ext)
		if path.Ext(parent) != ".json" {
			parent += ".json"
		}
		p, err := loadConfig(parent, readFile, depth+1)
		if err != nil {
			return loaded{}, err
		}
		if p.hasBase {
			l.baseURL, l.hasBase = p.baseURL, true
		}
		if p.paths != nil {
			l.paths, l.pathsDir = p.paths, p.pathsDir
		}
	}
	if b := doc.CompilerOptions.BaseURL; b != nil {
		l.baseURL, l.hasBase = path.Join(dir, *b), true
	}
	if doc.CompilerOptions.Paths != nil {
		l.paths, l.pathsDir = doc.CompilerOptions.Paths, dir
	}
	return l, nil
}

// stripJSONC removes comments and trailing commas, which tsconfig files allow and JSON does not.
func stripJSONC(src []byte) []byte {
	var out []byte
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inStr:
			out = append(out, c)
			if c == '\\' && i+1 < len(src) {
				i++
				out = append(out, src[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			for i += 2; i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/'); i++ {
			}
			i++
		case c == '}' || c == ']':
			j := len(out) - 1
			for j >= 0 && (out[j] == ' ' || out[j] == '\n' || out[j] == '\t' || out[j] == '\r') {
				j--
			}
			if j >= 0 && out[j] == ',' {
				out = append(out[:j], out[j+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
