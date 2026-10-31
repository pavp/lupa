package mapping

import (
	"reflect"
	"testing"
)

const userPatch = `diff --git a/src/user.ts b/src/user.ts
--- a/src/user.ts
+++ b/src/user.ts
@@ -10,3 +10,3 @@ import { db } from "./db";

-export function getUser(id: string) {
+export function fetchUser(id: string) {
   return db.users.get(id);
`

func TestClaims(t *testing.T) {
	files := []File{{Path: "src/user.ts"}, {Path: "src/legacy/cache.ts"}}
	patches := map[string]string{"src/user.ts": userPatch}
	cases := []struct {
		name string
		body string
		want []Claim
	}{
		{"a span found on an added line and on a removed line", "- Renames `getUser` to `fetchUser`", []Claim{{Text: "Renames `getUser` to `fetchUser`", Cites: []Cite{
			{Text: "getUser", Found: true, Path: "src/user.ts", Side: "-", Line: 11, Code: "export function getUser(id: string) {"},
			{Text: "fetchUser", Found: true, Path: "src/user.ts", Side: "+", Line: 11, Code: "export function fetchUser(id: string) {"},
		}}}},
		{"a path names a changed file", "Deletes src/legacy/cache.ts.", []Claim{{Text: "Deletes src/legacy/cache.ts.", Cites: []Cite{
			{Text: "src/legacy/cache.ts", Found: true, Path: "src/legacy/cache.ts"},
		}}}},
		{"a span the diff does not have", "Adds `retryLogin`.", []Claim{{Text: "Adds `retryLogin`.", Cites: []Cite{{Text: "retryLogin"}}}}},
		{"part of an identifier is not a match", "Touches `get`.", []Claim{{Text: "Touches `get`.", Cites: []Cite{{Text: "get"}}}}},
		{"prose is listed and not checked", "Improves login performance. Uses `fetchUser`.", []Claim{
			{Text: "Improves login performance.", Cites: nil},
			{Text: "Uses `fetchUser`.", Cites: []Cite{{Text: "fetchUser", Found: true, Path: "src/user.ts", Side: "+", Line: 11, Code: "export function fetchUser(id: string) {"}}},
		}},
		{"a span with spaces is a command or a snippet, not a citation", "Checks: `go test ./...`, `fetchUser`.", []Claim{{Text: "Checks: `go test ./...`, `fetchUser`.", Cites: []Cite{
			{Text: "fetchUser", Found: true, Path: "src/user.ts", Side: "+", Line: 11, Code: "export function fetchUser(id: string) {"},
		}}}},
		{"template leftovers, code blocks, HTML lines and links are not claims", "## Summary\n<!-- say why -->\n- [x] Tests added\n<details>\n<summary>Possible optimization</summary>\n```\nfetchUser(1)\n```\n</details>\nSee https://example.com/a/b.ts for context",
			[]Claim{{Text: "See https://example.com/a/b.ts for context", Cites: nil}}},
		{"an abbreviation does not end a sentence", "The max. of pending cancellations is 100. Once reached, sends fail.", []Claim{
			{Text: "The max. of pending cancellations is 100.", Cites: nil},
			{Text: "Once reached, sends fail.", Cites: nil},
		}},
		{"template fields, issue links and list lead-ins are not claims",
			"**Environment**: Staging(main branch) / Production\nFixes #7225\n- Closes #12, #13\nThe solution implemented in this PR:\n- Uses `fetchUser`:",
			[]Claim{{Text: "Uses `fetchUser`:", Cites: []Cite{{Text: "fetchUser", Found: true, Path: "src/user.ts", Side: "+", Line: 11, Code: "export function fetchUser(id: string) {"}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claims(c.body, files, patches); !reflect.DeepEqual(got, c.want) {
				t.Errorf("claims =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}

func TestClaimsOnlyForAPR(t *testing.T) {
	if s := Build(Input{Files: []File{{Path: "a.go"}}}); s.Claims != nil {
		t.Errorf("claims = %+v for a branch range, want none", s.Claims)
	}
	s := Build(Input{Files: []File{{Path: "a.go"}}, PR: &PR{Number: 1, Body: "Edits `a.go`"}})
	if len(s.Claims) != 1 || !s.Claims[0].Cites[0].Found {
		t.Errorf("claims = %+v, want one found cite", s.Claims)
	}
}

func TestClaimReceipts(t *testing.T) {
	files := []File{{Path: "src/a.ts"}, {Path: "src/schema.prisma"}, {Path: "src/cron/scheduleEmailReminders.ts"}}
	patches := map[string]string{
		"src/a.ts":          "diff --git a/src/a.ts b/src/a.ts\n--- a/src/a.ts\n+++ b/src/a.ts\n@@ -1,1 +1,2 @@\n x\n+// marks the reminder cancelled\n",
		"src/schema.prisma": "diff --git a/src/schema.prisma b/src/schema.prisma\n--- a/src/schema.prisma\n+++ b/src/schema.prisma\n@@ -1,1 +1,2 @@\n model R {\n+  cancelled Boolean?\n",
	}
	cases := []struct {
		name, body string
		want       Cite
	}{
		// A comment only mentions the name; the line that declares it supports the claim.
		{"a code line wins over a comment", "Adds a `cancelled` field.", Cite{Text: "cancelled", Found: true, Path: "src/schema.prisma", Side: "+", Line: 2, Code: "cancelled Boolean?"}},
		{"a changed file named without its extension", "Changes `scheduleEmailReminders`.", Cite{Text: "scheduleEmailReminders", Found: true, Path: "src/cron/scheduleEmailReminders.ts"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := claims(c.body, files, patches)
			if len(got) != 1 || len(got[0].Cites) != 1 || !reflect.DeepEqual(got[0].Cites[0], c.want) {
				t.Errorf("claims = %+v, want one cite %+v", got, c.want)
			}
		})
	}
}
