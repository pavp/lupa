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
		{"the run fails", "", errors.New("timeout"), []string{"Login is faster."}, 1, "claim check failed: timeout"},
		{"a malformed reply", "sure!", nil, []string{"Login is faster."}, 1, "claim check reply malformed"},
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

func TestCheckClaimsOverTheBudget(t *testing.T) {
	fake := &agent.Fake{Reply: func(agent.Request) (string, error) { return `{"results":[]}`, nil }}
	res := CheckClaims(context.Background(), ClaimsInput{Agent: fake, Sentences: []string{"Login is faster."},
		Patches: map[string]string{"big.ts": strings.Repeat("+x\n", DefaultBudget)}})
	if fake.Calls() != 0 || len(res.Checks) != 1 || res.Checks[0].Outcome != ClaimNotChecked || !strings.Contains(strings.Join(res.Notes, ""), "over") {
		t.Errorf("calls %d, result %+v; want no run and one claim not checked with a budget note", fake.Calls(), res)
	}
}
