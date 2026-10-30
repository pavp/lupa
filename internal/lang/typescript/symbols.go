package typescript

import (
	"slices"

	"github.com/pavp/lupa/internal/lang"
)

var _ lang.Symbols = Provider{}

// Decls returns the top-level declarations of src.
func (Provider) Decls(src []byte) []lang.Decl { return Decls(src) }

// Bindings returns the names src imports or re-exports from other modules.
func (Provider) Bindings(src []byte) []lang.Binding {
	bs, _ := bindings(lex(src))
	return bs
}

// Uses returns the lines of src that reference name.
func (Provider) Uses(src []byte, name string) []int { return Uses(src, name) }

// Decls returns the named top-level declarations of src in order, each spanning from its first token
// to the token before the next top-level statement. Statements are told apart by bracket depth and by
// line breaks where a semicolon could have been, so nested and continued code stays in its declaration.
func Decls(src []byte) []lang.Decl {
	toks := lex(src)
	starts := statements(toks)
	var out []lang.Decl
	for k, s := range starts {
		end := len(toks)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		if d, ok := decl(toks[s:end]); ok {
			d.Line, d.End = toks[s].line, toks[end-1].line
			out = append(out, d)
		}
	}
	return out
}

func statements(toks []token) []int {
	var out []int
	depth := 0
	for i, t := range toks {
		if depth == 0 && startsStatement(toks, i) {
			out = append(out, i)
		}
		if t.kind != punct {
			continue
		}
		switch t.text {
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth = max(depth-1, 0)
		}
	}
	return out
}

func startsStatement(toks []token, i int) bool {
	if i == 0 {
		return true
	}
	t, prev := toks[i], toks[i-1]
	switch {
	case t.kind == punct, t.kind == ident && continuesNext[t.text]:
		return false
	case prev.is(punct, ";"), prev.is(punct, "}"):
		return true
	case t.kind == str, t.line <= prev.line:
		return false
	case prev.kind == punct:
		return prev.text == ")" || prev.text == "]" || prev.text == "regex"
	}
	return !(prev.kind == ident && continuesPrev[prev.text])
}

// continuesNext are words that continue the statement before them; continuesPrev, the statement after them.
var (
	continuesNext = map[string]bool{
		"as": true, "satisfies": true, "extends": true, "implements": true, "instanceof": true, "in": true,
		"of": true, "else": true, "catch": true, "finally": true, "while": true,
	}
	continuesPrev = map[string]bool{
		"export": true, "default": true, "async": true, "declare": true, "abstract": true, "new": true,
		"await": true, "typeof": true, "keyof": true, "return": true, "extends": true, "implements": true,
		"as": true, "satisfies": true, "in": true, "of": true, "instanceof": true, "const": true, "let": true,
		"var": true, "function": true, "class": true, "type": true, "interface": true, "enum": true,
	}
	modifiers = map[string]bool{"export": true, "default": true, "async": true, "declare": true, "abstract": true}
)

// decl reads one top-level statement; ok is false when it declares no name.
func decl(toks []token) (lang.Decl, bool) {
	var d lang.Decl
	i := 0
	for ; i < len(toks) && toks[i].kind == ident && modifiers[toks[i].text]; i++ {
		d.Default = d.Default || toks[i].text == "default"
	}
	kw := at(toks, i)
	if kw.is(ident, "const") && at(toks, i+1).is(ident, "enum") {
		i++
		kw = toks[i]
	}
	name := at(toks, i+1)
	switch {
	case kw.is(ident, "function") && name.is(punct, "*"):
		name = at(toks, i+2)
	case kw.is(ident, "class") && (name.is(ident, "extends") || name.is(ident, "implements")):
		name = token{}
	}
	if kw.kind == ident && declaring[kw.text] && name.kind == ident {
		d.Name = name.text
	}
	if d.Name == "" && d.Default {
		d.Name = "default"
	}
	return d, d.Name != ""
}

var declaring = map[string]bool{
	"function": true, "class": true, "const": true, "let": true, "var": true, "interface": true,
	"type": true, "enum": true, "namespace": true, "module": true,
}

// Uses returns the sorted lines where name is referenced as an identifier, leaving out import and
// re-export clauses and member names (x.name); comments and strings never count.
func Uses(src []byte, name string) []int {
	toks := lex(src)
	_, inClause := bindings(toks)
	var lines []int
	for i, t := range toks {
		if t.is(ident, name) && !inClause[i] && !(i > 0 && toks[i-1].is(punct, ".")) {
			lines = append(lines, t.line)
		}
	}
	return slices.Compact(lines)
}

// bindings returns the names toks import or re-export from a module, and marks the tokens of those clauses.
func bindings(toks []token) ([]lang.Binding, []bool) {
	var out []lang.Binding
	inClause := make([]bool, len(toks))
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != ident || i > 0 && toks[i-1].is(punct, ".") || t.text != "import" && t.text != "export" {
			continue
		}
		if n := at(toks, i+1); t.text == "import" && (n.is(punct, "(") || n.is(punct, ".") || n.kind == str) {
			continue
		}
		bs, spec, ok := clause(toks, i+1, t.text == "export")
		if !ok {
			continue
		}
		out = append(out, bs...)
		for k := i; k <= spec; k++ {
			inClause[k] = true
		}
		i = spec
	}
	return out, inClause
}

// clause reads `a, { b as c, type D }`, `* as ns` or `{ x }` up to `from "<spec>"`, returning the bindings and
// the index of the specifier; ok is false when the statement takes nothing from a module.
func clause(toks []token, i int, reexport bool) ([]lang.Binding, int, bool) {
	var bs []lang.Binding
	inBraces := false
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t.is(ident, "from") && at(toks, i+1).kind == str:
			for k := range bs {
				bs[k].Spec, bs[k].Reexport = toks[i+1].text, reexport
			}
			return bs, i + 1, true
		case t.is(punct, "{"), t.is(punct, "}"):
			inBraces = t.text == "{"
		case t.is(punct, ","):
		case t.is(ident, "type") && (at(toks, i+1).is(punct, "{") || at(toks, i+1).is(punct, "*") ||
			at(toks, i+1).kind == ident && !at(toks, i+1).is(ident, "from")):
		case t.is(punct, "*"), t.kind == ident && (inBraces || !declares[t.text]):
			imported, local := t.text, t
			if at(toks, i+1).is(ident, "as") && at(toks, i+2).kind == ident {
				local = toks[i+2]
				i += 2
			}
			if t.kind == ident && !inBraces {
				imported = "default"
			}
			bs = append(bs, lang.Binding{Imported: imported, Local: local.text, Line: local.line})
		default:
			return nil, i, false
		}
	}
	return nil, i, false
}
