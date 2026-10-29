package report

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"api key assignment", `const API_KEY = "abcd1234efgh";`, `const API_KEY = "‹redacted›";`},
		{"api-key with colon", "api-key: abcd1234efgh", "api-key: ‹redacted›"},
		{"secret", "client_secret=s3cr3tvalue99", "client_secret=‹redacted›"},
		{"token", "GITHUB_TOKEN='tok_12345678'", "GITHUB_TOKEN='‹redacted›'"},
		{"password", "password: hunter2hunter2", "password: ‹redacted›"},
		{"anthropic key", "key := \"sk-ant-api03-AbCdEfGhIjKlMnOpQrStUv-wxyz_0123\"", "key := \"‹redacted›\""},
		{"openai project key", "OPENAI sk-proj-AbCdEfGhIjKlMnOpQrSt_uv-wx", "OPENAI ‹redacted›"},
		{"github fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz", "‹redacted›"},
		{"slack bot token", "xoxb-1234567890-0987654321-AbCdEfGhIjKl", "‹redacted›"},
		{"bearer header", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", "Authorization: Bearer ‹redacted›"},
		{"passwd", "passwd=correcthorse", "passwd=‹redacted›"},
		{"authorization", "Authorization: dXNlcjpwYXNzd29yZA==", "Authorization: ‹redacted›"},
		{"short value kept", "token: string", "token: string"},
		{"aws key", "id = AKIAIOSFODNN7EXAMPLE here", "id = ‹redacted› here"},
		{"github token", "use ghp_" + strings.Repeat("A1", 18) + " now", "use ‹redacted› now"},
		{"openai key", "key sk-" + strings.Repeat("x", 30), "key ‹redacted›"},
		{"private key block", "x\n-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----\ny", "x\n‹redacted›\ny"},
		{"unterminated private key", "-----BEGIN PRIVATE KEY-----\nMIIabc", "‹redacted›"},
		{"normal code", "for (let i = 1; i < items.length; i++) {", "for (let i = 1; i < items.length; i++) {"},
		{"word ending in sk", "const disk-abcdefghijklmnopqrstuvwxyz = 1", "const disk-abcdefghijklmnopqrstuvwxyz = 1"},
		{"token in a name", "const tokenCount = tokens.length", "const tokenCount = tokens.length"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Redact(c.in); got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
