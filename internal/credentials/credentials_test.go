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
		{"an unquoted password value", "password: hunter2hunter2", []Match{{"assigned-secret", Low}}},
		// Outside code, such as a .env or YAML file, a secret is written without quotes.
		{"an unquoted value outside code", "refresh_token: refreshToken", []Match{{"assigned-secret", Low}}},
		{"an interpolated value outside code", "DB_PASSWORD=${DB_PASSWORD_FROM_VAULT}", nil},
		{"a value read from the environment is no secret", "export const token = process.env.TOKEN", nil},
		{"a value read from a config field is no secret", "apiKey: config.stripeApiKey,", nil},
		{"a value returned by a call is no secret", "password = readPassword(path)", nil},
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

func TestRedactKeepsRedactingReferences(t *testing.T) {
	if got := Redact("token = process.env.TOKEN"); got != "token = ‹redacted›" {
		t.Errorf("Redact = %q: redaction stays conservative", got)
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

func TestFindInCodeReportsOnlyQuotedValuesNextToACredentialName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Match
	}{
		{"a field set from a variable", "refresh_token: refreshToken,", nil},
		{"a field set from a variable of the same name", "client_secret: client_secret,", nil},
		{"an assignment from a variable", "const password = userPassword", nil},
		{"a quoted literal", `password = "hunter2hunter2"`, []Match{{"assigned-secret", Low}}},
		{"a quoted literal in an object", `apiKey: 'abcd1234efgh5678',`, []Match{{"assigned-secret", Low}}},
		{"a structured key needs no quotes", "token: ghp_" + strings.Repeat("A1", 18), []Match{{"github-token", High}}},
		{"a backquoted literal", "apiKey: `abcd1234efgh5678`,", []Match{{"assigned-secret", Low}}},
		{"a template that interpolates a variable", "authorization: `${scheme} ${token}`,", nil},
		{"a placeholder equal to its key", `data.refresh_token = "refresh_token";`, nil},
		{"a literal after an interpolation", `password: "${prefix}tail12345",`, []Match{{"assigned-secret", Low}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FindInCode(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("FindInCode(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestRedactHidesAnUnquotedValueWithABackquote(t *testing.T) {
	for _, in := range []string{"password=abc`defghijk", "password=abcdefghij`klmnop"} {
		if got := Redact(in); got != "password="+redacted {
			t.Errorf("Redact(%q) = %q, want the whole value hidden", in, got)
		}
	}
}
