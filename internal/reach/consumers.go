package reach

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pavp/lupa/internal/verify"
)

// Snippet is unchanged code at head that consumes what a change produces, lines Line to End of File.
type Snippet struct {
	File      string
	Line, End int
	Why       string
	Code      string
}

// ConsumerInput is what Consumers needs: the repo files at head, the change's patches by path, the changed
// declarations, and optionally the direct importers of each changed file, read before other files.
type ConsumerInput struct {
	Files    []string
	ReadFile func(string) ([]byte, error)
	Patches  map[string]string
	Changed  []Symbol
	Direct   map[string][]string
	// Owns limits the search to files of the analyzed language; nil means every file.
	Owns func(string) bool
}

const (
	declarationWindow = 40
	sitesPerName      = 6
	namesPerFile      = 8
	siteContext       = 2
)

var (
	fieldWrite    = regexp.MustCompile(`(?:^|[{,(]\s*|\s)([A-Za-z_$][\w$]*)\??\s*:\s*[^:\s]`)
	propertyWrite = regexp.MustCompile(`\.([A-Za-z_$][\w$]*)\s*=[^=]`)
	constantName  = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{3,}\b`)
	comparison    = regexp.MustCompile(`[=!]==?|\bcase\b`)
	bareListItem  = regexp.MustCompile(`^[A-Za-z_$][\w$]*,?$`)
)

// Consumers returns, per changed file, unchanged code at head that consumes what its change produces: the rest
// of each changed declaration the patch does not show, the readers of field names whose written value the
// change touches, and the uses of constants its changed lines name. Matching is by name, so a dynamic access
// or a renamed destructuring is missed; sites in changed files come first, then direct importers, then the rest.
func Consumers(in ConsumerInput) (map[string][]Snippet, error) {
	shown := map[string]map[int]bool{}
	for p, patch := range in.Patches {
		shown[p] = verify.HunkLines(patch)
	}
	src := map[string][]string{}
	lines := func(p string) ([]string, error) {
		if l, ok := src[p]; ok {
			return l, nil
		}
		b, err := in.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		src[p] = strings.Split(string(b), "\n")
		return src[p], nil
	}

	out := map[string][]Snippet{}
	rest := map[string][]Snippet{}
	for _, d := range in.Changed {
		l, err := lines(d.File)
		if err != nil {
			return nil, err
		}
		if s, ok := restOf(d, l, shown[d.File]); ok {
			rest[d.File] = append(rest[d.File], s)
		}
	}

	type want struct {
		changed, name string
		constant      bool
	}
	var wants []want
	for _, p := range slices.Sorted(mapKeys(in.Patches)) {
		fields, constants := namesOf(in.Patches[p])
		for _, n := range fields {
			wants = append(wants, want{p, n, false})
		}
		for _, n := range constants {
			wants = append(wants, want{p, n, true})
		}
	}
	if len(wants) == 0 {
		return rest, nil
	}

	type site struct {
		file string
		line int
	}
	found := make([][]site, len(wants))
	declared := map[string]bool{}
	for _, f := range in.Files {
		if in.Owns != nil && !in.Owns(f) {
			continue
		}
		l, err := lines(f)
		if err != nil {
			return nil, err
		}
		hit := false
		for i, w := range wants {
			if w.constant && !declared[w.name] && declaresConstant(l, w.name) {
				declared[w.name] = true
			}
			for n, text := range l {
				if shown[f][n+1] || !holdsName(text, w.name) || isWrite(text, w.name, w.constant) {
					continue
				}
				found[i] = append(found[i], site{f, n + 1})
				hit = true
			}
		}
		if !hit && in.Patches[f] == "" {
			delete(src, f)
		}
	}
	type ranked struct {
		rank int
		s    Snippet
	}
	picked := map[string][]ranked{}
	seen := map[site]bool{}
	for i, w := range wants {
		if w.constant && !declared[w.name] {
			continue
		}
		// A constant is consumed where code compares it, which outranks where the file sits.
		rank := func(s site) int {
			r := 2
			switch {
			case in.Patches[s.file] != "":
				r = 0
			case slices.Contains(in.Direct[w.changed], s.file):
				r = 1
			}
			if w.constant && !comparison.MatchString(src[s.file][s.line-1]) {
				r += 5
			}
			return r
		}
		slices.SortStableFunc(found[i], func(a, b site) int { return rank(a) - rank(b) })
		why := "reads " + w.name
		if w.constant {
			why = "uses " + w.name
		}
		taken := 0
		for _, s := range found[i] {
			if taken == sitesPerName {
				break
			}
			l := src[s.file]
			from, to := max(1, s.line-siteContext), min(len(l), s.line+siteContext)
			if seen[s] || slices.ContainsFunc(picked[w.changed], func(o ranked) bool { return o.s.File == s.file && o.s.Line <= to && from <= o.s.End }) {
				continue
			}
			seen[s] = true
			taken++
			picked[w.changed] = append(picked[w.changed], ranked{rank(s), Snippet{File: s.file, Line: from, End: to, Why: why + " (line " + fmt.Sprint(s.line) + ")",
				Code: strings.Join(l[from-1:to], "\n")}})
		}
	}
	for f, list := range picked {
		slices.SortStableFunc(list, func(a, b ranked) int { return a.rank - b.rank })
		for _, r := range list {
			out[f] = append(out[f], r.s)
		}
	}
	// Readers come before the rest of a declaration, so a cut for space drops the rest first.
	for f, list := range rest {
		out[f] = append(out[f], list...)
	}
	return out, nil
}

// restOf is the part of declaration d the patch does not show, up to declarationWindow lines around its hunks.
func restOf(d Symbol, l []string, shown map[int]bool) (Snippet, bool) {
	first, last, hidden := 0, 0, false
	for n := d.Line; n <= d.End && n <= len(l); n++ {
		if shown[n] {
			if first == 0 {
				first = n
			}
			last = n
		} else {
			hidden = true
		}
	}
	if !hidden || first == 0 {
		return Snippet{}, false
	}
	from, to := max(d.Line, first-declarationWindow/2), min(d.End, last+declarationWindow/2, len(l))
	if to-from+1 > declarationWindow {
		to = from + declarationWindow - 1
	}
	return Snippet{File: d.File, Line: from, End: to, Why: "rest of changed declaration " + d.Name, Code: strings.Join(l[from-1:to], "\n")}, true
}

// namesOf returns the field names written on both a removed and an added line of the patch, whose written value
// the change touches, and the constants its changed lines name; names of 3 characters or fewer, and comment
// lines, are left out.
func namesOf(patch string) (fields, constants []string) {
	minus, plus := map[string]bool{}, map[string]bool{}
	for _, c := range verify.ChangedLines(patch) {
		if isComment(c.Text) {
			continue
		}
		set := plus
		if c.Side == "-" {
			set = minus
		}
		for _, re := range []*regexp.Regexp{fieldWrite, propertyWrite} {
			for _, m := range re.FindAllStringSubmatch(c.Text, -1) {
				if len(m[1]) > 3 {
					set[m[1]] = true
				}
			}
		}
		for _, m := range constantName.FindAllString(c.Text, -1) {
			if !slices.Contains(constants, m) {
				constants = append(constants, m)
			}
		}
	}
	for _, n := range slices.Sorted(mapKeys(plus)) {
		if minus[n] && !constantName.MatchString(n) {
			fields = append(fields, n)
		}
	}
	return fields[:min(namesPerFile, len(fields))], constants[:min(namesPerFile, len(constants))]
}

// isWrite reports a line that writes or declares name rather than reading it: an object key or property
// assignment for a field; an import or a declaration for a constant.
func isWrite(line, name string, constant bool) bool {
	t := strings.TrimSpace(line)
	if isComment(t) {
		return true
	}
	q := regexp.QuoteMeta(name)
	if constant {
		return strings.HasPrefix(t, "import ") || strings.Contains(t, "} from ") || bareListItem.MatchString(t) ||
			regexp.MustCompile(`\b(?:const|let|var)\s+`+q+`\b`).MatchString(t)
	}
	return regexp.MustCompile(`(?:^|[{,]\s*)`+q+`\??\s*:[^:]`).MatchString(t) || regexp.MustCompile(`\.`+q+`\s*=[^=]`).MatchString(t)
}

// declaresConstant reports whether the file declares name, so a language global such as JSON is not searched.
func declaresConstant(l []string, name string) bool {
	re := regexp.MustCompile(`\b(?:const|let|var|enum)\s+` + regexp.QuoteMeta(name) + `\b|^\s*` + regexp.QuoteMeta(name) + `\s*[:=][^=]`)
	for _, line := range l {
		if strings.Contains(line, name) && re.MatchString(line) {
			return true
		}
	}
	return false
}

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "#")
}

// holdsName reports whether line contains name as a whole identifier.
func holdsName(line, name string) bool {
	for from := 0; ; {
		i := strings.Index(line[from:], name)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(name)
		if (i == 0 || !isIdentByte(line[i-1])) && (end == len(line) || !isIdentByte(line[end])) {
			return true
		}
		from = i + 1
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func mapKeys[V any](ms ...map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		seen := map[string]bool{}
		for _, m := range ms {
			for k := range m {
				if !seen[k] {
					seen[k] = true
					if !yield(k) {
						return
					}
				}
			}
		}
	}
}
