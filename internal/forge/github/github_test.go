package github

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
		ok   bool
	}{
		{"482", Ref{Number: 482}, true},
		{"#482", Ref{Number: 482}, true},
		{"https://github.com/acme/shop/pull/482", Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 482}, true},
		{"https://github.com/acme/shop/pull/482/files", Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 482}, true},
		{"https://ghe.example.com/acme/shop/pull/7?x=1#c", Ref{Host: "ghe.example.com", Owner: "acme", Repo: "shop", Number: 7}, true},
		{"feature", Ref{}, false},
		{"main..feature", Ref{}, false},
		{"0", Ref{}, false},
		{"-3", Ref{}, false},
		{"https://github.com/acme/shop/issues/482", Ref{}, false},
		{"https://github.com/acme/pull/482", Ref{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseRef(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

const prJSON = `{"number":482,"url":"https://github.com/acme/shop/pull/482","title":"Fix cart totals","body":"Closes #12","baseRefName":"main","headRefName":"fix-cart","headRefOid":"abc123","closingIssuesReferences":[{"number":12}],"files":[{"path":"src/cart.ts"},{"path":"src/tax.ts"}]}`

// fakeGh writes an executable that records its arguments and working directory into dir, then runs body.
func fakeGh(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable needs /bin/sh")
	}
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + dir + "/args'\npwd > '" + dir + "/pwd'\n" + body + "\n"
	bin := filepath.Join(dir, "gh")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestPR(t *testing.T) {
	fields := "number,url,title,body,baseRefName,headRefName,headRefOid,closingIssuesReferences,files"
	tests := []struct {
		name string
		ref  Ref
		arg  string
	}{
		{"number, from the clone", Ref{Number: 482}, "482"},
		{"url", Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 482}, "https://github.com/acme/shop/pull/482"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := fakeGh(t, dir, "printf '%s' '"+prJSON+"'")

			pr, err := Client{Bin: bin}.PR(dir, tt.ref)
			if err != nil {
				t.Fatalf("PR: %v", err)
			}
			if got, want := lines(t, filepath.Join(dir, "args")), []string{"pr", "view", tt.arg, "--json", fields}; !slices.Equal(got, want) {
				t.Errorf("args = %q, want %q", got, want)
			}
			want := PR{Ref: Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 482}, Title: "Fix cart totals", Body: "Closes #12",
				BaseRef: "main", HeadRef: "fix-cart", HeadSHA: "abc123", LinkedIssues: 1, Files: []string{"src/cart.ts", "src/tax.ts"}}
			if pr.Ref != want.Ref || pr.Title != want.Title || pr.Body != want.Body || pr.BaseRef != want.BaseRef || pr.HeadRef != want.HeadRef ||
				pr.HeadSHA != want.HeadSHA || pr.LinkedIssues != want.LinkedIssues || !slices.Equal(pr.Files, want.Files) {
				t.Errorf("PR = %+v, want %+v", pr, want)
			}
		})
	}
}

func TestPRErrors(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"gh fails", "echo 'no pull requests found' >&2; exit 1", "no pull requests found"},
		{"not json", "echo 'oops'", "gh pr view"},
		{"no head", `printf '%s' '{"number":1,"url":"https://github.com/a/b/pull/1","baseRefName":"main"}'`, "head"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := Client{Bin: fakeGh(t, dir, tt.body)}.PR(dir, Ref{Number: 1})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestSameRepo(t *testing.T) {
	ref := Ref{Host: "github.com", Owner: "Acme", Repo: "shop", Number: 1}
	for _, tt := range []struct {
		remote string
		want   bool
	}{
		{"https://github.com/acme/shop.git", true},
		{"https://github.com/acme/shop", true},
		{"git@github.com:acme/shop.git", true},
		{"ssh://git@github.com/acme/shop.git", true},
		{"https://github.com/acme/shop-fork.git", false},
		{"https://gitlab.com/acme/shop.git", false},
		{"/tmp/upstream.git", false},
	} {
		if got := SameRepo(tt.remote, ref); got != tt.want {
			t.Errorf("SameRepo(%q) = %v, want %v", tt.remote, got, tt.want)
		}
	}
}

func TestOpenPRs(t *testing.T) {
	dir := t.TempDir()
	bin := fakeGh(t, dir, `printf '%s' '[{"number":9,"title":"Cart fix","baseRefName":"main","headRefName":"cart","files":[{"path":"src/a.ts"}]},{"number":7,"title":"Self","files":[]}]'`)
	prs, err := Client{Bin: bin}.OpenPRs(Ref{Host: "github.com", Owner: "acme", Repo: "shop", Number: 7})
	if err != nil {
		t.Fatalf("OpenPRs: %v", err)
	}
	want := []string{"pr", "list", "-R", "github.com/acme/shop", "--state", "open", "--limit", "200", "--json", "number,title,baseRefName,headRefName,files"}
	if got := lines(t, filepath.Join(dir, "args")); !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
	if len(prs) != 2 || prs[0].Ref.Number != 9 || prs[0].Title != "Cart fix" || prs[0].BaseRef != "main" || prs[0].HeadRef != "cart" || !slices.Equal(prs[0].Files, []string{"src/a.ts"}) {
		t.Errorf("open PRs = %+v", prs)
	}
}
