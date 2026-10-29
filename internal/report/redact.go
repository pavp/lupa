package report

import "regexp"

const redacted = "‹redacted›"

var (
	privateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`)
	tokens     = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}|\bghp_[A-Za-z0-9]{36}|\bsk-[A-Za-z0-9]{20,}`)
	assigned   = regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|password|passwd|authorization)\s*[:=]\s*['"]?)[^\s'"]{8,}`)
)

// Redact replaces values that look like credentials; an assigned value keeps its key name.
func Redact(s string) string {
	s = privateKey.ReplaceAllString(s, redacted)
	s = tokens.ReplaceAllString(s, redacted)
	return assigned.ReplaceAllString(s, "${1}"+redacted)
}
