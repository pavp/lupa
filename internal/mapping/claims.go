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
	// templateField is a bold label filled in from a PR template, such as `**Environment**: Staging`.
	templateField = regexp.MustCompile(`^\*\*[^*]+\*\*\s*:`)
	issueLinks    = regexp.MustCompile(`(?i)^(?:(?:fix|fixe[sd]|close[sd]?|resolve[sd]?|refs?|related to)\s+)?#\d+(?:\s*,\s*#\d+)*\.?$`)
)

// claims cuts the description into sentences and list items, skipping HTML comments, headings,
// checkboxes, fenced code, lines that open with an HTML tag, such as a <details> block's markup,
// template fields, issue links and a lead-in to a list that cites nothing, and looks up every code
// span and path each one cites in the diff.
func claims(body string, files []File, patches map[string]string) []Claim {
	var out []Claim
	fenced := false
	for _, line := range strings.Split(htmlComment.ReplaceAllString(body, ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced || boilerplate.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "<") {
			continue
		}
		item := strings.TrimSpace(listMarker.ReplaceAllString(line, ""))
		if templateField.MatchString(item) || issueLinks.MatchString(item) {
			continue
		}
		for _, text := range sentences(item) {
			c := Claim{Text: text}
			cited := citesOf(text, files)
			for i, name := range cited {
				others := slices.Concat(cited[:i], cited[i+1:])
				c.Cites = append(c.Cites, lookUp(name, others, files, patches))
			}
			if len(c.Cites) == 0 && strings.HasSuffix(text, ":") {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

// sentences splits a line after `.`, `!` or `?` followed by a space, never inside a code span nor before a
// lowercase letter, which follows an abbreviation such as "max." rather than a sentence end.
func sentences(line string) []string {
	var out []string
	start, inCode := 0, false
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '`':
			inCode = !inCode
		case !inCode && strings.ContainsRune(".!?", rune(line[i])) && i+1 < len(line) && line[i+1] == ' ' && !startsLower(line[i+1:]):
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

// lookUp finds a cited text as a changed file (also named without its extension), else on a changed line of
// the diff: a code line wins over a comment, which only mentions the name; then the line whose hunk points where
// the claim does, holding more of the claim's other cites (header included, in any case) or lying in a file the
// claim names; then a line that declares the name over one that uses it; then the first by path order.
func lookUp(cited string, others []string, files []File, patches map[string]string) Cite {
	for _, f := range files {
		if namesFile(cited, f.Path) {
			return Cite{Text: cited, Found: true, Path: f.Path}
		}
	}
	paths := make([]string, 0, len(patches))
	for p := range patches {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	best, bestRank := Cite{Text: cited}, -1
	for _, p := range paths {
		for _, h := range hunks(patches[p]) {
			rank := 2 * pointedAt(p, h, others)
			for _, l := range verify.ChangedLines(h) {
				if !holdsToken(l.Text, cited) {
					continue
				}
				r := rank
				if declares(l.Text, cited) {
					r++
				}
				if !isComment(l.Text) {
					r += 2*len(others) + 2
				}
				if r > bestRank {
					best, bestRank = Cite{Text: cited, Found: true, Path: p, Side: l.Side, Line: l.N, Code: l.Text}, r
				}
			}
		}
	}
	return best
}

// pointedAt counts the other cites of a claim that point at this hunk of path: a cite naming the file, or a
// cite the hunk holds, its header included, in any case.
func pointedAt(path, hunk string, others []string) int {
	lower := strings.ToLower(hunk)
	n := 0
	for _, o := range others {
		if namesFile(o, path) || holdsToken(lower, strings.ToLower(o)) {
			n++
		}
	}
	return n
}

// declares reports a line that introduces name rather than uses it: after a declaring keyword, as a method or a
// typed field at the start of the line, as a schema field (`name Type`), or as an SQL column or table.
func declares(line, name string) bool {
	n := regexp.QuoteMeta(name)
	for _, pattern := range []string{
		`\b(?:function|const|let|var|class|interface|type|enum|def|func|struct)\s+\*?` + n + `\b`,
		`^(?:(?:export|default|public|private|protected|static|async|readonly|override|abstract)\s+)*` + n + `\s*(?:<[^>]*>)?\(.*\)\s*(?::[^=]*)?\{\s*$`,
		`^(?:(?:public|private|protected|static|readonly)\s+)*` + n + `\??\s*:\s*[A-Za-z_][\w<>\[\]|. ]*;?$`,
		`^` + n + `\s+[\w.\[\]*]+\??(?:\s+@.*)?$`,
		`(?i)\b(?:add\s+column|create\s+table|create\s+type)\s+(?:if\s+not\s+exists\s+)?["'\x60]?` + n + `["'\x60]?(?:\s|$|\()`,
	} {
		if regexp.MustCompile(pattern).MatchString(line) {
			return true
		}
	}
	return false
}

func namesFile(cited, p string) bool {
	base := path.Base(p)
	return p == cited || strings.HasSuffix(p, "/"+cited) || !strings.ContainsAny(cited, "./") && strings.TrimSuffix(base, path.Ext(base)) == cited
}

// hunks splits a unified diff into its hunks, each with its `@@` header.
func hunks(patch string) []string {
	var out []string
	var cur []string
	for _, l := range strings.Split(patch, "\n") {
		if strings.HasPrefix(l, "@@") {
			if cur != nil {
				out = append(out, strings.Join(cur, "\n"))
			}
			cur = []string{l}
		} else if cur != nil {
			cur = append(cur, l)
		}
	}
	if cur != nil {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

// isComment reports a line that is only a comment in the common line and block comment syntaxes.
func isComment(line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range []string{"//", "#", "/*", "*", "<!--", "--"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
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

func startsLower(s string) bool {
	s = strings.TrimLeft(s, " ")
	return s != "" && s[0] >= 'a' && s[0] <= 'z'
}
