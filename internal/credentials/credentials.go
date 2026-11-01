// Package credentials recognizes secrets in text. Each pattern carries its confidence as data, so a
// reader that only redacts can take every match while one that reports a finding can tell them apart.
package credentials

import (
	"regexp"
	"strings"
)

// Confidence is how sure a match is to be a real credential.
type Confidence string

const (
	// High is a value with a provider's known structure, such as a prefix and a length.
	High Confidence = "high"
	// Low is a value that only sits next to a credential-like name, or a published example key.
	Low Confidence = "low"
	// none marks a pattern that is redacted but never reported, such as a provider's test key.
	none Confidence = ""
)

const redacted = "‹redacted›"

type pattern struct {
	kind       string
	re         *regexp.Regexp
	confidence Confidence
	// keep is whether Redact leaves the first submatch in place before the value: the key name or the header.
	keep bool
}

// patterns run in order; a structured value comes before the name-based patterns that would also catch it.
var patterns = []pattern{
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`), High, false},
	{"aws-access-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}`), High, false},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36}|\bgithub_pat_[A-Za-z0-9_]{20,}`), High, false},
	{"openai-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), High, false},
	{"stripe-live-key", regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{16,}`), High, false},
	{"stripe-test-key", regexp.MustCompile(`\b[rs]k_test_[A-Za-z0-9]{16,}`), none, false},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), High, false},
	{"bearer-token", regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{16,}`), Low, true},
	{"assigned-secret", regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password|passwd|authorization)\s*[:=]\s*['"` + "`" + `]?)[^\s'"]{8,}`), Low, true},
}

// examples are published sample credentials, matched as exact literals only: a heuristic such as
// "contains EXAMPLE" would let a real key through.
var examples = map[string]bool{
	"AKIAIOSFODNN7EXAMPLE": true,
}

// Match is one credential found in a text.
type Match struct {
	Kind       string
	Confidence Confidence
}

// Find returns the credentials in s that are worth reporting, in pattern order. A value an earlier
// pattern already matched is not reported again, and a published example key is Low.
func Find(s string) []Match { return find(s, false) }

// FindInCode is Find for a line of source code, where a credential-like name set to an unquoted value
// assigns a variable, not a secret: only a quoted literal or a provider's structure is reported.
func FindInCode(s string) []Match { return find(s, true) }

func find(s string, code bool) []Match {
	var out []Match
	var taken [][2]int
	for _, p := range patterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(s, -1) {
			start := m[0]
			if p.keep {
				start = m[3]
			}
			if overlaps(taken, start, m[1]) {
				continue
			}
			taken = append(taken, [2]int{start, m[1]})
			if p.confidence == none || p.keep && reference(s, m) || p.kind == "assigned-secret" && (placeholder(s, m) || code && !quoted(s, m)) {
				continue
			}
			c := p.confidence
			if examples[s[start:m[1]]] && !continues(s, m[1]) {
				c = Low
			}
			out = append(out, Match{Kind: p.kind, Confidence: c})
		}
	}
	return out
}

// referenceValue is an unquoted value that names where a credential comes from instead of holding it:
// process.env.TOKEN, config.apiKey, readPassword(path).
var referenceValue = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[\w$]|\()`)

// reference reports a name-based match whose value is an unquoted reference, which Find does not report;
// Redact still replaces it, since redaction stays cheap to get wrong.
func reference(s string, m []int) bool {
	return !quoted(s, m) && referenceValue.MatchString(s[m[3]:m[1]])
}

// quoted reports a name-based match whose value opens with a quote.
func quoted(s string, m []int) bool {
	prefix := s[m[2]:m[3]]
	return strings.HasSuffix(prefix, `"`) || strings.HasSuffix(prefix, "'") || strings.HasSuffix(prefix, "`")
}

// interpolation is a value that is one ${NAME} and nothing else but closing punctuation.
var interpolation = regexp.MustCompile("^\\$\\{[^}]+\\}[`,;)]*$")

// placeholder reports an assigned value that holds no secret: an interpolation such as ${DB_PASSWORD},
// or the key's own name, such as refresh_token = "refresh_token".
func placeholder(s string, m []int) bool {
	value := s[m[3]:m[1]]
	if interpolation.MatchString(value) {
		return true
	}
	start := m[2]
	for start > 0 && (s[start-1] == '_' || continues(s, start-1)) {
		start--
	}
	key := s[start:m[2]] + strings.TrimRight(s[m[2]:m[3]], " \t:='\"`")
	return strings.EqualFold(value, key)
}

// continues reports a letter or digit at i, where a match ended inside a longer value.
func continues(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	c := s[i]
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func overlaps(spans [][2]int, start, end int) bool {
	for _, sp := range spans {
		if start < sp[1] && sp[0] < end {
			return true
		}
	}
	return false
}

// Redact replaces every value that looks like a credential, test and example keys included; an
// assigned value keeps its key name and a bearer token its header.
func Redact(s string) string {
	for _, p := range patterns {
		repl := redacted
		if p.keep {
			repl = "${1}" + redacted
		}
		s = p.re.ReplaceAllString(s, repl)
	}
	return s
}
