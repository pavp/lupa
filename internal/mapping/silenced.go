package mapping

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/pavp/lupa/internal/verify"
)

// Site is a changed line the map points at.
type Site struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Code string `json:"code"`
}

var (
	silencers = []*regexp.Regexp{
		regexp.MustCompile(`\bcatch\s*(\([^)]*\))?\s*\{\s*\}`),
		regexp.MustCompile(`\.catch\(\s*(\(\s*[\w$]*\s*\)|[\w$]+)\s*=>\s*(\{\s*\}|null|undefined|void 0)\s*\)`),
		regexp.MustCompile(`^except(\s+[\w.(), ]+)?\s*:\s*pass$`),
		regexp.MustCompile(`^_\s*=\s*err\b`),
	}
	openCatch = regexp.MustCompile(`\bcatch\s*(\([^)]*\))?\s*\{$`)
	// orTrue is a shell command forced to succeed; in code, `x || true` is a default value instead.
	orTrue = regexp.MustCompile(`\|\|\s*true\b`)
)

func isShellLike(p string) bool {
	base := strings.ToLower(path.Base(p))
	ext := path.Ext(base)
	return ext == ".sh" || ext == ".bash" || ext == ".yml" || ext == ".yaml" || base == "makefile" || strings.HasPrefix(base, "dockerfile")
}

// silencedErrors finds added lines that swallow an error: an empty catch, a .catch that returns nothing,
// `|| true` in shell and CI files, `except: pass`, `_ = err`. Generated files and tests are left out.
func silencedErrors(files []File, patches map[string]string) []Site {
	var out []Site
	for _, f := range files {
		if f.Class == Skip || isTestPath(f.Path) {
			continue
		}
		var added []verify.Changed
		for _, c := range verify.ChangedLines(patches[f.Path]) {
			if c.Side == "+" {
				added = append(added, c)
			}
		}
		for i, c := range added {
			hit := slices.ContainsFunc(silencers, func(re *regexp.Regexp) bool { return re.MatchString(c.Text) }) ||
				isShellLike(f.Path) && orTrue.MatchString(c.Text)
			// A catch opened on one line and closed on the next added line is empty too.
			if !hit && openCatch.MatchString(c.Text) && i+1 < len(added) && added[i+1].N == c.N+1 && strings.TrimSpace(added[i+1].Text) == "}" {
				hit = true
			}
			if hit {
				out = append(out, Site{File: f.Path, Line: c.N, Code: c.Text})
			}
		}
	}
	slices.SortFunc(out, func(a, b Site) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		return a.Line - b.Line
	})
	return out
}
