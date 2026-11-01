package credentials

import (
	"reflect"
	"strings"
	"testing"
)

func TestFind(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Match
	}{
		{"sk- is an OpenAI key, not Stripe", "key sk-proj-" + strings.Repeat("Ab", 12), []Match{{"openai-key", High}}},
		{"Stripe live secret key", `stripe = "sk_live_` + strings.Repeat("Ab1", 8) + `"`, []Match{{"stripe-live-key", High}}},
		{"Stripe live restricted key", "rk_live_" + strings.Repeat("Ab1", 8), []Match{{"stripe-live-key", High}}},
		// Stripe test keys move no real money, so they are no finding; Stripe still advises keeping them private.
		{"Stripe test secret key is no finding", "sk_test_" + strings.Repeat("Ab1", 8), nil},
		{"Stripe test restricted key is no finding", "rk_test_" + strings.Repeat("Ab1", 8), nil},
		{"AWS's published example key drops to low", "id = AKIAIOSFODNN7EXAMPLE", []Match{{"aws-access-key", Low}}},
		{"a value that only starts with the example key stays high", "id = AKIAIOSFODNN7EXAMPLE2X", []Match{{"aws-access-key", High}}},
		{"an AWS access key", "id = AKIAZ7Q2RS4TUV6WX8YA", []Match{{"aws-access-key", High}}},
		{"a GitHub token", "ghp_" + strings.Repeat("A1", 18), []Match{{"github-token", High}}},
		{"a GitHub fine-grained token", "github_pat_11ABCDEFG0123456789_abcdefghij", []Match{{"github-token", High}}},
		{"a Slack token", "xoxb-1234567890-0987654321-AbCdEfGhIjKl", []Match{{"slack-token", High}}},
		{"a private key block", "-----BEGIN RSA PRIVATE KEY-----\nMIIabc", []Match{{"private-key", High}}},
		{"a structured key under a credential name is reported once", `api_key = "sk_live_` + strings.Repeat("Ab1", 8) + `"`, []Match{{"stripe-live-key", High}}},
		{"a password assignment", `password = "hunter2hunter2"`, []Match{{"assigned-secret", Low}}},
		{"a bearer header", "Authorization: Bearer abcdefghijklmnopqrstuv", []Match{{"bearer-token", Low}}},
		{"a hash is no secret", `etag := "e3b0c44298fc1c149afbf4c8996fb924"`, nil},
		{"plain code", "for (let i = 1; i < items.length; i++) {", nil},
		{"a name containing token", "const tokenCount = tokens.length", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Find(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Find(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestRedactCoversEveryKey(t *testing.T) {
	for _, in := range []string{
		"sk_live_" + strings.Repeat("Ab1", 8),
		"rk_live_" + strings.Repeat("Ab1", 8),
		"sk_test_" + strings.Repeat("Ab1", 8),
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("A1", 18),
	} {
		if got := Redact("k " + in + " end"); got != "k ‹redacted› end" {
			t.Errorf("Redact(%q) = %q", in, got)
		}
	}
	if got := Redact(`password = "hunter2hunter2"`); got != `password = "‹redacted›"` {
		t.Errorf("assigned value: %q", got)
	}
}
