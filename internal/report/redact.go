package report

import "github.com/pavp/lupa/internal/credentials"

// Redact replaces values that look like credentials; an assigned value keeps its key name.
func Redact(s string) string { return credentials.Redact(s) }
