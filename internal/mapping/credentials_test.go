package mapping

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/credentials"
)

var githubToken = "ghp_" + strings.Repeat("A1", 18)

func credentialPatches() ([]File, map[string]string) {
	files := []File{{Path: "fixtures/user.json", Additions: 2}, {Path: "src/config.ts", Additions: 3, Deletions: 1}}
	patches := map[string]string{
		"fixtures/user.json": "diff --git a/fixtures/user.json b/fixtures/user.json\n@@ -0,0 +1,2 @@\n+{\n+  \"token\": \"" + githubToken + "\"\n",
		"src/config.ts":      "diff --git a/src/config.ts b/src/config.ts\n@@ -1,2 +1,4 @@\n export const config = {\n-  key: \"" + githubToken + "\",\n+  password: \"hunter2hunter2\",\n+  stripe: \"sk_test_" + strings.Repeat("Ab1", 8) + "\",\n+  region: \"eu\",\n",
	}
	return files, patches
}

func TestCredentialSites(t *testing.T) {
	files, patches := credentialPatches()
	s := Build(Input{Base: "b", Head: "h", Files: files, Patches: patches})
	want := []CredentialSite{
		// Structure wins over the path: a live token in a fixture is still leaked.
		{File: "fixtures/user.json", Line: 2, Kind: "github-token", Confidence: credentials.High},
		{File: "src/config.ts", Line: 2, Kind: "assigned-secret", Confidence: credentials.Low},
	}
	if !reflect.DeepEqual(s.Credentials, want) {
		t.Errorf("credentials = %+v, want %+v", s.Credentials, want)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), githubToken) || strings.Contains(string(out), "hunter2") {
		t.Errorf("the map carries a credential's value:\n%s", out)
	}
}

func TestGateHandsBackALiveCredential(t *testing.T) {
	files, patches := credentialPatches()
	files[1].Additions += 500
	g := Build(Input{Base: "b", Head: "h", Files: files, Patches: patches}).ReviewNow
	if g.Decision != HandBack || !strings.Contains(g.Reason, "github-token at fixtures/user.json:2") || !strings.Contains(g.Reply, "rotate") {
		t.Errorf("review_now = %+v, want a hand-back for the token, ahead of the size", g)
	}

	patches["fixtures/user.json"] = "diff --git a/fixtures/user.json b/fixtures/user.json\n@@ -0,0 +1 @@\n+{}\n"
	files[1].Additions -= 500
	if g := Build(Input{Base: "b", Head: "h", Files: files, Patches: patches}).ReviewNow; g.Decision != Review {
		t.Errorf("review_now = %+v, want review: a password assignment alone is only a possible secret", g)
	}
}

func TestCredentialSitesTellCodeFromConfig(t *testing.T) {
	files := []File{{Path: "src/oauth.ts", Additions: 1}, {Path: ".env.example", Additions: 1}}
	patches := map[string]string{
		"src/oauth.ts": "diff --git a/src/oauth.ts b/src/oauth.ts\n@@ -0,0 +1 @@\n+  refresh_token: refreshToken,\n",
		".env.example": "diff --git a/.env.example b/.env.example\n@@ -0,0 +1 @@\n+PASSWORD=hunter2hunter2\n",
	}
	s := Build(Input{Base: "b", Head: "h", Files: files, Patches: patches})
	want := []CredentialSite{{File: ".env.example", Line: 1, Kind: "assigned-secret", Confidence: credentials.Low}}
	if !reflect.DeepEqual(s.Credentials, want) {
		t.Errorf("credentials = %+v, want only the unquoted value in the env file, not the variable in code", s.Credentials)
	}
}
