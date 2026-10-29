// Package typescript is the TypeScript/JavaScript language provider.
package typescript

// Imports returns the module specifiers a source file imports, in order, each once: static imports,
// re-exports, dynamic import() and require() with a literal string. Comments, strings, template text
// and regular expressions are skipped, so text that only looks like an import is ignored.
func Imports(src []byte) []string {
	toks := lex(src)
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != ident || (i > 0 && toks[i-1].is(punct, ".")) {
			continue
		}
		switch t.text {
		case "import":
			switch {
			case at(toks, i+1).is(punct, "("):
				if s := at(toks, i+2); s.kind == str {
					add(s.text)
				}
			case at(toks, i+1).kind == str:
				add(toks[i+1].text)
			default:
				if s, ok := fromClause(toks, i+1); ok {
					add(s)
				}
			}
		case "export":
			if s, ok := fromClause(toks, i+1); ok {
				add(s)
			}
		case "require":
			if at(toks, i+1).is(punct, "(") && at(toks, i+2).kind == str {
				add(toks[i+2].text)
			}
		}
	}
	return out
}

// fromClause reads `{ a, b as c }`, `* as ns`, `type X`, `x` up to `from "<specifier>"`.
// Anything else, such as `export const`, means the statement has no source module.
func fromClause(toks []token, i int) (string, bool) {
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.is(ident, "from") && at(toks, i+1).kind == str:
			return toks[i+1].text, true
		case t.kind == ident && declares[t.text]:
			return "", false
		case t.kind == ident, t.is(punct, "{"), t.is(punct, "}"), t.is(punct, ","), t.is(punct, "*"):
		default:
			return "", false
		}
	}
	return "", false
}

var declares = map[string]bool{
	"const": true, "let": true, "var": true, "function": true, "class": true, "default": true,
	"interface": true, "enum": true, "async": true, "abstract": true, "declare": true, "namespace": true,
}

type kind byte

const (
	ident kind = iota + 1
	str
	punct
)

type token struct {
	kind kind
	text string
}

func (t token) is(k kind, text string) bool { return t.kind == k && t.text == text }

func at(toks []token, i int) token {
	if i < len(toks) {
		return toks[i]
	}
	return token{}
}

type lexer struct {
	src  []byte
	pos  int
	toks []token
}

func lex(src []byte) []token {
	l := &lexer{src: src}
	l.run(false)
	return l.toks
}

// run lexes until the end of input or, inside a template `${…}`, until its closing brace.
func (l *lexer) run(inTemplate bool) {
	depth := 0
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			l.pos++
		case c == '/' && l.peek(1) == '/':
			l.skipUntil("\n")
		case c == '/' && l.peek(1) == '*':
			l.pos += 2
			l.skipUntil("*/")
		case c == '\'' || c == '"':
			l.toks = append(l.toks, token{str, l.quoted(c)})
		case c == '`':
			l.template()
		case c == '/' && l.regexAllowed():
			l.regex()
		case c >= '0' && c <= '9':
			for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) || l.src[l.pos] == '.') {
				l.pos++
			}
			l.toks = append(l.toks, token{ident, "0"})
		case isIdentStart(c):
			start := l.pos
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			l.toks = append(l.toks, token{ident, string(l.src[start:l.pos])})
		default:
			l.pos++
			if inTemplate && c == '{' {
				depth++
			}
			if inTemplate && c == '}' {
				if depth == 0 {
					return
				}
				depth--
			}
			l.toks = append(l.toks, token{punct, string(c)})
		}
	}
}

func (l *lexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func (l *lexer) skipUntil(end string) {
	for l.pos < len(l.src) && string(l.src[l.pos:min(l.pos+len(end), len(l.src))]) != end {
		l.pos++
	}
	l.pos += len(end)
}

func (l *lexer) quoted(q byte) string {
	l.pos++
	var b []byte
	for l.pos < len(l.src) && l.src[l.pos] != q && l.src[l.pos] != '\n' {
		if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
			l.pos++
		}
		b = append(b, l.src[l.pos])
		l.pos++
	}
	l.pos++
	return string(b)
}

// template skips the literal text of a template string and lexes its `${…}` expressions.
func (l *lexer) template() {
	l.pos++
	for l.pos < len(l.src) && l.src[l.pos] != '`' {
		switch {
		case l.src[l.pos] == '\\':
			l.pos += 2
		case l.src[l.pos] == '$' && l.peek(1) == '{':
			l.pos += 2
			l.run(true)
		default:
			l.pos++
		}
	}
	l.pos++
	l.toks = append(l.toks, token{str, ""})
}

// regexAllowed tells a regular expression from a division by the previous token.
func (l *lexer) regexAllowed() bool {
	if len(l.toks) == 0 {
		return true
	}
	prev := l.toks[len(l.toks)-1]
	switch prev.kind {
	case punct:
		return prev.text != ")" && prev.text != "]"
	case ident:
		return regexKeywords[prev.text]
	}
	return false
}

var regexKeywords = map[string]bool{
	"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true,
	"new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true,
}

func (l *lexer) regex() {
	l.pos++
	inClass := false
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		c := l.src[l.pos]
		switch {
		case c == '\\':
			l.pos++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			l.pos++
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			l.toks = append(l.toks, token{punct, "regex"})
			return
		}
		l.pos++
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
