// Package owners reads a CODEOWNERS file the way GitHub matches it: the last matching rule wins.
package owners

import (
	"regexp"
	"strings"
)

type rule struct {
	re     *regexp.Regexp
	owners []string
}

// File is a parsed CODEOWNERS file.
type File struct{ rules []rule }

// Parse reads CODEOWNERS content; comments and blank lines are ignored. A pattern with no owners
// marks its files as explicitly unowned.
func Parse(src []byte) File {
	var f File
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		f.rules = append(f.rules, rule{re: compile(fields[0]), owners: fields[1:]})
	}
	return f
}

// Owners returns the owners of path, nil when no rule matches or the matching rule names none.
func (f File) Owners(path string) []string {
	for i := len(f.rules) - 1; i >= 0; i-- {
		if f.rules[i].re.MatchString(path) {
			if len(f.rules[i].owners) == 0 {
				return nil
			}
			return f.rules[i].owners
		}
	}
	return nil
}

// compile turns a gitignore-style pattern into a regexp over repo-relative paths. A pattern with a
// slash before its end is anchored to the root; otherwise it matches at any depth. A pattern ending
// in `/*` matches only direct children, as GitHub documents for CODEOWNERS.
func compile(pattern string) *regexp.Regexp {
	p := strings.TrimSuffix(pattern, "/")
	anchored := strings.HasPrefix(p, "/") || strings.Contains(strings.TrimPrefix(p, "/"), "/")
	p = strings.TrimPrefix(p, "/")
	directOnly := strings.HasSuffix(p, "/*")

	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("^(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case p[i] == '*':
			b.WriteString("[^/]*")
		case p[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(p[i : i+1]))
		}
	}
	if directOnly {
		b.WriteString("$")
	} else {
		b.WriteString("(?:/.*)?$")
	}
	return regexp.MustCompile(b.String())
}
