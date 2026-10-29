package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/agent"
)

const loginPatch = `diff --git a/src/login.ts b/src/login.ts
--- a/src/login.ts
+++ b/src/login.ts
@@ -4,2 +4,2 @@
-const user = await db.users.find(id);
+const user = await cache.users.get(id);
 return user;
`

const totalPatch = `diff --git a/src/cart.ts b/src/cart.ts
--- a/src/cart.ts
+++ b/src/cart.ts
@@ -1,1 +1,1 @@
-export const total = 0;
+export const total = sum(items);
`

func checkClaims(t *testing.T, reply string, err error, sentences ...string) (ClaimsResult, *agent.Fake) {
	t.Helper()
	fake := &agent.Fake{Reply: func(agent.Request) (string, error) { return reply, err }}
	res := CheckClaims(context.Background(), ClaimsInput{Agent: fake, Sentences: sentences,
		Patches: map[string]string{"src/login.ts": loginPatch, "src/cart.ts": totalPatch}})
	return res, fake
}

func TestCheckClaims(t *testing.T) {
	const login = "Login reads the user from the cache."
	const cart = "The cart.ts total now sums the items."
	cases := []struct {
		name     string
		sentence string
		reply    string
		want     ClaimCheck
		note     string
	}{
		{"a supported claim with a changed line of its file", login,
			`{"results":[{"id":0,"claim":"Login reads the user from the cache","outcome":"supported","file":"src/login.ts","quote":"const user = await cache.users.get(id);"}]}`,
			ClaimCheck{Sentence: login, Claim: "Login reads the user from the cache", Outcome: ClaimSupported, File: "src/login.ts", Side: "+", Line: 4, Code: "const user = await cache.users.get(id);"}, ""},
		{"a quote the diff does not have", login,
			`{"results":[{"id":0,"claim":"Login reads the user from the cache","outcome":"supported","file":"src/login.ts","quote":"return cache.get(id);"}]}`,
			ClaimCheck{Sentence: login, Claim: "Login reads the user from the cache", Outcome: ClaimUnsupported}, "quote is not a changed line of src/login.ts"},
		{"a quote outside the file the sentence names", cart,
			`{"results":[{"id":0,"claim":"The cart.ts total now sums the items","outcome":"supported","file":"src/login.ts","quote":"const user = await cache.users.get(id);"}]}`,
			ClaimCheck{Sentence: cart, Claim: "The cart.ts total now sums the items", Outcome: ClaimUnsupported}, "the sentence names src/cart.ts"},
		{"a claim that cannot be judged from a diff", "Login is faster.",
			`{"results":[{"id":0,"claim":"Login is faster","outcome":"not_judgeable"}]}`,
			ClaimCheck{Sentence: "Login is faster.", Claim: "Login is faster", Outcome: ClaimNotJudgeable}, ""},
		{"no supporting line", login,
			`{"results":[{"id":0,"claim":"Login reads the user from the cache","outcome":"unsupported"}]}`,
			ClaimCheck{Sentence: login, Claim: "Login reads the user from the cache", Outcome: ClaimUnsupported}, ""},
		{"a paraphrased claim is not trusted", login,
			`{"results":[{"id":0,"claim":"Users come from cache","outcome":"supported","file":"src/login.ts","quote":"const user = await cache.users.get(id);"}]}`,
			ClaimCheck{Sentence: login, Outcome: ClaimNotChecked}, "claim is not part of its sentence"},
		{"no result for the sentence", login, `{"results":[]}`,
			ClaimCheck{Sentence: login, Outcome: ClaimNotChecked}, "id 0: no result"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _ := checkClaims(t, c.reply, nil, c.sentence)
			if len(res.Checks) != 1 || res.Checks[0] != c.want {
				t.Errorf("checks = %+v, want %+v", res.Checks, c.want)
			}
			if notes := strings.Join(res.Notes, "\n"); c.note == "" && notes != "" || !strings.Contains(notes, c.note) {
				t.Errorf("notes = %q, want %q", notes, c.note)
			}
		})
	}
}

func TestCheckClaimsWithoutAUsableRun(t *testing.T) {
	cases := []struct {
		name, reply string
		err         error
		sentences   []string
		calls       int
		note        string
	}{
		{"nothing to check", "", nil, nil, 0, ""},
		{"the run fails", "", errors.New("timeout"), []string{"Login is faster."}, 1, "claim check: failed: timeout"},
		{"a malformed reply", "sure!", nil, []string{"Login is faster."}, 1, "claim check: reply malformed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, fake := checkClaims(t, c.reply, c.err, c.sentences...)
			if fake.Calls() != c.calls || res.Calls != c.calls || !strings.Contains(strings.Join(res.Notes, "\n"), c.note) {
				t.Errorf("calls = %d/%d, notes = %q; want %d calls and %q", fake.Calls(), res.Calls, res.Notes, c.calls, c.note)
			}
			for _, ch := range res.Checks {
				if ch.Outcome != ClaimNotChecked {
					t.Errorf("check = %+v, want not checked", ch)
				}
			}
		})
	}
}

// bigPatch is one hunk of about the budget, so the login patch and it go in separate chunks.
var bigPatch = "diff --git a/z.ts b/z.ts\n--- a/z.ts\n+++ b/z.ts\n@@ -1,0 +1,1 @@\n+export const big = 1;\n" + strings.Repeat(" filler\n", DefaultBudget/8)

func TestCheckClaimsInChunks(t *testing.T) {
	const login = "Login reads the user from the cache."
	result := func(outcome, file, quote string) string {
		if outcome == "" {
			return `{"results":[]}`
		}
		return `{"results":[{"id":0,"claim":"Login reads the user from the cache","outcome":"` + outcome + `","file":"` + file + `","quote":"` + quote + `"}]}`
	}
	cases := []struct {
		name          string
		first, second string
		secondErr     error
		outcome, file string
	}{
		{"supported in one chunk is supported", result("unsupported", "", ""), result("supported", "src/login.ts", "const user = await cache.users.get(id);"), nil, ClaimSupported, "src/login.ts"},
		{"unsupported only when every chunk says so", result("unsupported", "", ""), result("unsupported", "", ""), nil, ClaimUnsupported, ""},
		{"a failed chunk leaves an unsupported claim not checked", result("unsupported", "", ""), "", errors.New("timeout"), ClaimNotChecked, ""},
		{"not judgeable in one chunk wins over unsupported", result("not_judgeable", "", ""), result("unsupported", "", ""), nil, ClaimNotJudgeable, ""},
		{"a chunk's invented quote does not support", result("unsupported", "", ""), result("supported", "z.ts", "export const login = cache;"), nil, ClaimUnsupported, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var prompts []string
			fake := &agent.Fake{Reply: func(req agent.Request) (string, error) {
				prompts = append(prompts, req.Prompt)
				if strings.Contains(req.Prompt, "cache.users.get") {
					return c.first, nil
				}
				return c.second, c.secondErr
			}}
			res := CheckClaims(context.Background(), ClaimsInput{Agent: fake, Sentences: []string{login},
				Patches: map[string]string{"src/login.ts": loginPatch, "z.ts": bigPatch}})
			if len(prompts) != 2 || strings.Contains(prompts[0], "export const big") == strings.Contains(prompts[1], "export const big") || res.Calls != 2 {
				t.Fatalf("%d runs, %d calls; want 2, each with its own part of the diff", len(prompts), res.Calls)
			}
			if got := res.Checks[0]; got.Outcome != c.outcome || got.File != c.file {
				t.Errorf("check = %+v, want %s %s", got, c.outcome, c.file)
			}
		})
	}
}

func TestClaimChunksSplitAFileAtItsHunks(t *testing.T) {
	hunk := "@@ -1,1 +1,1 @@\n+" + strings.Repeat("x", DefaultBudget/2) + "\n"
	patch := "diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n" + hunk + hunk + hunk
	chunks := claimChunks(map[string]string{"a.ts": patch})
	if len(chunks) != 3 {
		t.Fatalf("%d chunks, want 3", len(chunks))
	}
	for _, ch := range chunks {
		if !strings.HasPrefix(ch, "diff --git a/a.ts b/a.ts\n") || strings.Count(ch, "@@ -1") != 1 {
			t.Errorf("chunk does not carry the file header and one hunk:\n%.80s", ch)
		}
	}
}
