package typescript

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// workspacePkg is a package of the repo's own monorepo, resolvable by name without node_modules.
type workspacePkg struct {
	name    string
	dir     string
	main    string
	exports json.RawMessage
}

// conditionOrder prefers source-like entry points; `types` is skipped because declarations are not code.
var conditionOrder = []string{"source", "import", "require", "node", "default"}

// loadWorkspaces reads the workspace globs from package.json (npm, yarn) or pnpm-workspace.yaml and
// the name, main and exports of every package.json they match.
func loadWorkspaces(files []string, readFile func(string) ([]byte, error)) ([]workspacePkg, error) {
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	var globs []string
	if raw, err := readFile("package.json"); has["package.json"] && err == nil {
		var root struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("package.json: %w", err)
		}
		var list []string
		var obj struct {
			Packages []string `json:"packages"`
		}
		if json.Unmarshal(root.Workspaces, &list) == nil {
			globs = append(globs, list...)
		} else if json.Unmarshal(root.Workspaces, &obj) == nil {
			globs = append(globs, obj.Packages...)
		}
	}
	if raw, err := readFile("pnpm-workspace.yaml"); has["pnpm-workspace.yaml"] && err == nil {
		globs = append(globs, pnpmPackages(string(raw))...)
	}
	if len(globs) == 0 {
		return nil, nil
	}
	var pkgs []workspacePkg
	for _, f := range files {
		dir := path.Dir(f)
		if path.Base(f) != "package.json" || dir == "." || strings.Contains(f, "node_modules/") || !matchesAny(globs, dir) {
			continue
		}
		raw, err := readFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		var p struct {
			Name    string          `json:"name"`
			Main    string          `json:"main"`
			Exports json.RawMessage `json:"exports"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if p.Name != "" {
			pkgs = append(pkgs, workspacePkg{name: p.Name, dir: dir, main: p.Main, exports: p.Exports})
		}
	}
	return pkgs, nil
}

// pnpmPackages reads the `packages:` list of a pnpm-workspace.yaml without a YAML parser.
func pnpmPackages(src string) []string {
	var out []string
	inList := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "packages:"):
			inList = true
		case inList && strings.HasPrefix(trimmed, "- "):
			out = append(out, strings.Trim(strings.TrimSpace(trimmed[2:]), `'"`))
		case trimmed != "" && !strings.HasPrefix(line, " "):
			inList = false
		}
	}
	return out
}

func matchesAny(globs []string, dir string) bool {
	for _, g := range globs {
		g = strings.TrimSuffix(strings.TrimPrefix(g, "./"), "/")
		if strings.HasSuffix(g, "/**") && strings.HasPrefix(dir, strings.TrimSuffix(g, "**")) {
			return true
		}
		if ok, _ := path.Match(g, dir); ok {
			return true
		}
	}
	return false
}

// resolveWorkspace resolves a bare specifier naming a workspace package; ok is false when no package matches.
func (r *Resolver) resolveWorkspace(spec string) (file string, status Status, ok bool) {
	var pkg workspacePkg
	for _, p := range r.workspaces {
		if (spec == p.name || strings.HasPrefix(spec, p.name+"/")) && len(p.name) > len(pkg.name) {
			pkg = p
		}
	}
	if pkg.name == "" {
		return "", 0, false
	}
	sub := "." + strings.TrimPrefix(spec, pkg.name)
	var candidates []string
	switch {
	case len(pkg.exports) > 0:
		candidates = exportTargets(pkg.exports, sub)
	case sub == ".":
		if pkg.main != "" {
			candidates = append(candidates, pkg.main)
		}
		candidates = append(candidates, ".")
	default:
		candidates = []string{sub}
	}
	best := Unresolved
	for _, c := range candidates {
		f, st := r.target(path.Join(pkg.dir, c))
		if st == Resolved {
			return f, Resolved, true
		}
		best = min(best, st)
	}
	return "", best, true
}

// exportTargets returns the files an `exports` field maps sub to, in condition order.
func exportTargets(exports json.RawMessage, sub string) []string {
	var str string
	if json.Unmarshal(exports, &str) == nil {
		if sub == "." {
			return []string{str}
		}
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(exports, &m) != nil {
		return nil
	}
	isSubpathMap := false
	for k := range m {
		isSubpathMap = isSubpathMap || strings.HasPrefix(k, ".")
	}
	if !isSubpathMap {
		if sub != "." {
			return nil
		}
		return conditionTargets(exports, "")
	}
	if v, ok := m[sub]; ok {
		return conditionTargets(v, "")
	}
	for k, v := range m {
		if prefix, suffix, ok := strings.Cut(k, "*"); ok && strings.HasPrefix(sub, prefix) && strings.HasSuffix(sub, suffix) {
			return conditionTargets(v, strings.TrimSuffix(strings.TrimPrefix(sub, prefix), suffix))
		}
	}
	return nil
}

func conditionTargets(v json.RawMessage, star string) []string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return []string{strings.Replace(s, "*", star, 1)}
	}
	var conds map[string]json.RawMessage
	if json.Unmarshal(v, &conds) != nil {
		return nil
	}
	var out []string
	for _, c := range conditionOrder {
		if next, ok := conds[c]; ok {
			out = append(out, conditionTargets(next, star)...)
		}
	}
	return out
}
