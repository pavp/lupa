package mapping

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/pavp/lupa/internal/verify"
)

// Claim is one sentence or list item of a PR description. Cites are the code spans and paths it names;
// a claim without any is prose and is not checked.
type Claim struct {
	Text  string `json:"text"`
	Cites []Cite `json:"cites"`
}

// Cite is a code span or path a claim names, and where the diff has it: a changed file (no Side), or
// the first added (`+`) or removed (`-`) line holding it as a whole token. Found is false when neither.
type Cite struct {
	Text  string `json:"text"`
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
	Side  string `json:"side,omitempty"`
	Line  int    `json:"line,omitempty"`
	Code  string `json:"code,omitempty"`
}

// NotFoundAsWritten reports a missed span with a dot and no slash, such as a field path a diff spells
// differently: a search cannot say the claim is false, so `lupa review` asks the model about it.
func (c Cite) NotFoundAsWritten() bool {
	return !c.Found && strings.Contains(c.Text, ".") && !strings.Contains(c.Text, "/")
}

// Unchecked reports whether no search could settle the claim: it is prose, or a cite is not found as written.
func (c Claim) Unchecked() bool {
	return len(c.Cites) == 0 || slices.ContainsFunc(c.Cites, Cite.NotFoundAsWritten)
}

var (
	codeSpan   = regexp.MustCompile("`([^`]+)`")
	webLink    = regexp.MustCompile(`https?://\S+`)
	filePath   = regexp.MustCompile(`(?:[\w.-]+/)+[\w.-]*\.[A-Za-z0-9]+`)
	fileName   = regexp.MustCompile(`[\w-]+\.[A-Za-z][A-Za-z0-9]*`)
	listMarker = regexp.MustCompile(`^\s*(?:>\s*)*(?:[-*+]|\d+[.)])\s+`)
)

// claims cuts the description into sentences and list items, skipping HTML comments, headings,
// checkboxes and fenced code, and looks up every code span and path each one cites in the diff.
func claims(body string, files []File, patches map[string]string) []Claim {
	var out []Claim
	fenced := false
	for _, line := range strings.Split(htmlComment.ReplaceAllString(body, ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced || boilerplate.MatchString(line) {
			continue
		}
		for _, text := range sentences(listMarker.ReplaceAllString(line, "")) {
			c := Claim{Text: text}
			for _, cited := range citesOf(text, files) {
				c.Cites = append(c.Cites, lookUp(cited, files, patches))
			}
			out = append(out, c)
		}
	}
	return out
}

// sentences splits a line after `.`, `!` or `?` followed by a space, never inside a code span.
func sentences(line string) []string {
	var out []string
	start, inCode := 0, false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '`':
			inCode = !inCode
		case !inCode && strings.ContainsRune(".!?", rune(line[i])) && i+1 < len(line) && line[i+1] == ' ':
			out = appendSentence(out, line[start:i+1])
			start = i + 1
		}
	}
	return appendSentence(out, line[start:])
}

func appendSentence(out []string, s string) []string {
	if s = strings.TrimSpace(s); s != "" {
		out = append(out, s)
	}
	return out
}

// citesOf returns the code spans and file paths of a claim in the order they appear, once each; paths
// inside spans and links do not count, nor do spans with spaces, which hold commands and snippets. A bare file name counts only when it names a changed file, so
// "Node.js" is not a citation.
func citesOf(text string, files []File) []string {
	type hit struct {
		at   int
		text string
	}
	var hits []hit
	for _, m := range codeSpan.FindAllStringSubmatchIndex(text, -1) {
		if span := strings.TrimSpace(text[m[2]:m[3]]); !strings.ContainsAny(span, " \t") {
			hits = append(hits, hit{m[0], span})
		}
	}
	prose := blank(text, codeSpan, webLink)
	for _, m := range filePath.FindAllStringIndex(prose, -1) {
		hits = append(hits, hit{m[0], prose[m[0]:m[1]]})
	}
	prose = blank(prose, filePath)
	for _, m := range fileName.FindAllStringIndex(prose, -1) {
		name := prose[m[0]:m[1]]
		if slices.ContainsFunc(files, func(f File) bool { return path.Base(f.Path) == name }) {
			hits = append(hits, hit{m[0], name})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int { return a.at - b.at })
	var out []string
	for _, h := range hits {
		if h.text != "" && !slices.Contains(out, h.text) {
			out = append(out, h.text)
		}
	}
	return out
}

// blank replaces every match of the patterns with spaces, keeping the other characters where they were.
func blank(s string, patterns ...*regexp.Regexp) string {
	for _, p := range patterns {
		s = p.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat(" ", len(m)) })
	}
	return s
}

// lookUp finds a cited text as a changed file, else on the first changed line of the diff, by path order.
func lookUp(cited string, files []File, patches map[string]string) Cite {
	for _, f := range files {
		if f.Path == cited || strings.HasSuffix(f.Path, "/"+cited) {
			return Cite{Text: cited, Found: true, Path: f.Path}
		}
	}
	paths := make([]string, 0, len(patches))
	for p := range patches {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		for _, l := range verify.ChangedLines(patches[p]) {
			if holdsToken(l.Text, cited) {
				return Cite{Text: cited, Found: true, Path: p, Side: l.Side, Line: l.N, Code: l.Text}
			}
		}
	}
	return Cite{Text: cited}
}

// holdsToken reports whether line contains s with no identifier character glued to an identifier edge
// of s, so `get` does not match getUser.
func holdsToken(line, s string) bool {
	for from := 0; ; {
		i := strings.Index(line[from:], s)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(s)
		if !(isIdent(s[0]) && i > 0 && isIdent(line[i-1])) && !(isIdent(s[len(s)-1]) && end < len(line) && isIdent(line[end])) {
			return true
		}
		from = i + 1
	}
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}
