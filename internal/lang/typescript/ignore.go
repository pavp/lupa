package typescript

import (
	"path"
	"sort"
	"strings"
)

type ignoreRule struct {
	dir, pattern      string
	anchored, negated bool
}

// ignoreRules reads every .gitignore in the repo, shallowest first so a nested file's rules come later and win.
func ignoreRules(files []string, readFile func(string) ([]byte, error)) []ignoreRule {
	var gitignores []string
	for _, f := range files {
		if path.Base(f) == ".gitignore" && !strings.Contains(f, "node_modules/") {
			gitignores = append(gitignores, f)
		}
	}
	sort.Slice(gitignores, func(i, j int) bool {
		di, dj := strings.Count(gitignores[i], "/"), strings.Count(gitignores[j], "/")
		return di < dj || di == dj && gitignores[i] < gitignores[j]
	})
	var rules []ignoreRule
	for _, f := range gitignores {
		raw, err := readFile(f)
		if err != nil {
			continue
		}
		dir := path.Dir(f)
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			r := ignoreRule{dir: dir}
			if r.negated = strings.HasPrefix(line, "!"); r.negated {
				line = line[1:]
			}
			line = strings.TrimSuffix(line, "/")
			r.anchored = strings.Contains(line, "/")
			r.pattern = strings.TrimPrefix(line, "/")
			rules = append(rules, r)
		}
	}
	return rules
}

// ignored reports whether git ignores p or a directory above it; the last matching rule decides, as in git.
// `**` is matched as a single segment, so a rule that relies on it may be missed.
func ignored(rules []ignoreRule, p string) bool {
	out := false
	for _, r := range rules {
		rel := p
		if r.dir != "." {
			if !strings.HasPrefix(p, r.dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(p, r.dir+"/")
		}
		if matchesPathOrParent(r, rel) {
			out = !r.negated
		}
	}
	return out
}

func matchesPathOrParent(r ignoreRule, rel string) bool {
	for p := rel; p != "." && p != ""; p = path.Dir(p) {
		subject := p
		if !r.anchored {
			subject = path.Base(p)
		}
		if ok, _ := path.Match(r.pattern, subject); ok {
			return true
		}
	}
	return false
}
