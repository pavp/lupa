package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo returns a repo whose `feature` branch changes a.txt (+2 −1) and adds b.go (+3) on top of main.
func newRepo(t *testing.T) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	base = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "a.txt", "one\n2\nthree\n")
	write(t, dir, "b.go", "package b\n\nvar B = 1\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "change")
	head = gitCmd(t, dir, "rev-parse", "HEAD")
	gitCmd(t, dir, "switch", "-q", "main")
	return dir, base, head
}

func lupa(dir string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, Env{Dir: dir, Stdout: &out, Stderr: &errOut})
	return code, out.String(), errOut.String()
}

type signals struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Files  []struct {
		Path      string `json:"path"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	} `json:"files"`
	Size struct {
		Files     int `json:"files"`
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Changed   int `json:"changed"`
	} `json:"size"`
}

func TestMapJSON(t *testing.T) {
	dir, base, head := newRepo(t)
	gitCmd(t, dir, "commit", "-q", "--allow-empty", "-m", "main moves on")
	for _, target := range []string{"main..feature", "feature"} {
		t.Run(target, func(t *testing.T) {
			code, stdout, stderr := lupa(dir, "map", target, "--json")
			if code != 0 {
				t.Fatalf("exit %d, stderr: %s", code, stderr)
			}
			var got signals
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
			}
			if got.Schema != "lupa.signals/v0" || got.Base != base || got.Head != head {
				t.Errorf("schema/base/head = %q %q %q, want lupa.signals/v0 %q (merge-base) %q", got.Schema, got.Base, got.Head, base, head)
			}
			if len(got.Files) != 2 ||
				got.Files[0].Path != "a.txt" || got.Files[0].Additions != 2 || got.Files[0].Deletions != 1 ||
				got.Files[1].Path != "b.go" || got.Files[1].Additions != 3 || got.Files[1].Deletions != 0 {
				t.Errorf("files = %+v", got.Files)
			}
			if got.Size.Files != 2 || got.Size.Additions != 5 || got.Size.Deletions != 1 || got.Size.Changed != 6 {
				t.Errorf("size = %+v", got.Size)
			}
		})
	}
}

func TestMapBinaryFile(t *testing.T) {
	dir, _, _ := newRepo(t)
	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "logo.png", "\x89PNG\x00\x01\x02")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "logo")
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, `"path": "logo.png",`) || !strings.Contains(stdout, `"binary": true`) {
		t.Errorf("binary file not reported as binary:\n%s", stdout)
	}
}

func TestMapClassifiesAndExcludesGenerated(t *testing.T) {
	dir, _, _ := newRepo(t)
	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "package-lock.json", "{\n\"lockfileVersion\": 3\n}\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "lock")
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Files []struct {
			Path   string `json:"path"`
			Class  string `json:"class"`
			Risk   string `json:"risk"`
			Reason string `json:"reason"`
		} `json:"files"`
		Size struct {
			Files          int `json:"files"`
			Changed        int `json:"changed"`
			GeneratedFiles int `json:"generated_files"`
		} `json:"size"`
		ReadingOrder []struct {
			Path    string `json:"path"`
			Section string `json:"section"`
		} `json:"reading_order"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if n := len(got.ReadingOrder); n != 3 || got.ReadingOrder[2].Path != "package-lock.json" || got.ReadingOrder[2].Section != "skip" {
		t.Errorf("reading_order = %+v, want 3 entries ending with the lockfile in skip", got.ReadingOrder)
	}
	if got.Size.Files != 2 || got.Size.Changed != 6 || got.Size.GeneratedFiles != 1 {
		t.Errorf("size = %+v, want 2 files, 6 changed lines, 1 generated file", got.Size)
	}
	lock := got.Files[2]
	if lock.Path != "package-lock.json" || lock.Class != "skip" || lock.Risk != "none" || lock.Reason != "lockfile" {
		t.Errorf("lockfile = %+v, want class skip, risk none, reason lockfile", lock)
	}
}

func TestMapText(t *testing.T) {
	dir, _, head := newRepo(t)
	code, stdout, stderr := lupa(dir, "map", "feature")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	saved := filepath.Join(gitCmd(t, dir, "rev-parse", "--absolute-git-dir"), "lupa", "feature", head[:7])
	for _, want := range []string{
		"**Review now? Yes** — 6 changed lines, generated excluded, within 400",
		"**Reading order**",
		"Saved: " + saved + " · next time: only what changed since " + head[:7],
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	md, err := os.ReadFile(filepath.Join(saved, "map.md"))
	if err != nil || !strings.HasPrefix(stdout, string(md)) {
		t.Errorf("map.md (%v) must be the printed report without the Saved line", err)
	}
}

func TestErrors(t *testing.T) {
	dir, _, _ := newRepo(t)
	cases := []struct {
		name       string
		dir        string
		args       []string
		wantStderr string
	}{
		{"no command", dir, nil, "usage: lupa map"},
		{"unknown command", dir, []string{"nope"}, "unknown command \"nope\""},
		{"map without target", dir, []string{"map"}, "usage: lupa map"},
		{"unknown flag", dir, []string{"map", "feature", "--yaml"}, "unknown flag \"--yaml\""},
		{"unknown ref", dir, []string{"map", "main..missing"}, "unknown revision \"missing\""},
		{"not a git repo", t.TempDir(), []string{"map", "feature"}, "not a git repository"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := lupa(c.dir, c.args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, c.wantStderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2, no stdout, stderr containing %q", code, stdout, stderr, c.wantStderr)
			}
		})
	}
}

func TestMapReach(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "tsconfig.json", `{ "compilerOptions": { "paths": { "@/*": ["src/*"] } } }`)
	write(t, dir, "src/Badge.ts", "export const badge = 1;\n")
	write(t, dir, "src/a.ts", "import { badge } from './Badge';\nexport const a = badge;\n")
	write(t, dir, "src/b.ts", "import { badge } from '@/Badge';\nexport const b = badge;\n")
	write(t, dir, "src/index.ts", "export * from './a';\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "src/Badge.ts", "import { gone } from './missing';\nexport const badge = 2;\n")
	write(t, dir, "src/b.ts", "import { badge } from '@/Badge';\nexport const b = badge + 1;\n")
	gitCmd(t, dir, "commit", "-q", "-am", "change")

	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Files []struct {
			Path      string   `json:"path"`
			Class     string   `json:"class"`
			Reason    string   `json:"reason"`
			Importers []string `json:"importers"`
			FirstLine int      `json:"first_line"`
		} `json:"files"`
		Unresolved []struct {
			From      string `json:"from"`
			Specifier string `json:"specifier"`
			Line      int    `json:"line"`
		} `json:"unresolved"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	badge := got.Files[0]
	if badge.FirstLine != 1 || badge.Path != "src/Badge.ts" || badge.Class != "read-first" || badge.Reason != "1 call site of its changed declarations outside this change" ||
		strings.Join(badge.Importers, ",") != "src/a.ts,src/index.ts" {
		t.Errorf("Badge = %+v, want read-first by its call site, importers src/a.ts and src/index.ts (src/b.ts is in the change)", badge)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].From != "src/Badge.ts" || got.Unresolved[0].Specifier != "./missing" || got.Unresolved[0].Line != 1 {
		t.Errorf("unresolved = %+v, want ./missing from src/Badge.ts", got.Unresolved)
	}
}

func TestMapAnImportOnlyEditIsNotRankedByItsImporters(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/util.ts", "export const one = 1;\nexport const two = 2;\n")
	write(t, dir, "src/builder.ts", "import { one } from './util';\nexport const build = () => one;\n")
	for _, f := range []string{"a", "b", "c"} {
		write(t, dir, "src/"+f+".ts", "import { build } from './builder';\nexport const "+f+" = build();\n")
	}
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "src/builder.ts", "import { one, two } from './util';\nexport const build = () => one;\n")
	gitCmd(t, dir, "commit", "-q", "-am", "change")

	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Files []struct {
			Path   string `json:"path"`
			Class  string `json:"class"`
			Reason string `json:"reason"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || strings.Contains(got.Files[0].Reason, "imported by") || got.Files[0].Class == "read-first" {
		t.Errorf("files = %+v, want builder.ts not ranked by its 3 importers: its change touches no declaration", got.Files)
	}
}

func TestMapSkipsCodeMarkedGeneratedByHeaderOrAttributes(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	for _, d := range []string{"api", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, dir, ".gitattributes", "api/*.ts linguist-generated=true\nsrc/keep.ts -linguist-generated\n")
	write(t, dir, "src/a.ts", "export const a = 1;\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "api/client.ts", "export const client = 1;\n")
	write(t, dir, "src/models.go", "// Code generated by sqlc. DO NOT EDIT.\npackage src\n")
	write(t, dir, "src/keep.ts", "export const keep = 1;\n")
	write(t, dir, "src/a.ts", "export const a = 2;\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "change")

	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Files []struct {
			Path   string `json:"path"`
			Class  string `json:"class"`
			Reason string `json:"reason"`
		} `json:"files"`
		Size struct {
			Changed        int `json:"changed"`
			GeneratedFiles int `json:"generated_files"`
		} `json:"size"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	class := map[string]string{}
	for _, f := range got.Files {
		class[f.Path] = f.Class + ": " + f.Reason
	}
	if class["api/client.ts"] != "skip: generated code (.gitattributes linguist-generated)" ||
		class["src/models.go"] != "skip: generated code (header)" || strings.HasPrefix(class["src/keep.ts"], "skip") {
		t.Errorf("classes = %v, want api/client.ts and src/models.go skipped as generated, src/keep.ts not", class)
	}
	if got.Size.GeneratedFiles != 2 || got.Size.Changed != 3 {
		t.Errorf("size = %+v, want 2 generated files and 3 authored changed lines (keep.ts 1, a.ts 2)", got.Size)
	}
}

func TestMapShowsImportsOfIgnoredPathsWithoutAskingTheAuthor(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".gitignore", "src/generated\n")
	write(t, dir, "src/a.ts", "export const a = 1;\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	write(t, dir, "src/a.ts", "import { Role } from './generated/enums';\nexport const a = Role.Admin;\n")
	gitCmd(t, dir, "commit", "-q", "-am", "change")

	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Unresolved []json.RawMessage `json:"unresolved"`
		Ignored    []struct {
			From      string `json:"from"`
			Specifier string `json:"specifier"`
			Line      int    `json:"line"`
		} `json:"ignored_imports"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Unresolved) != 0 || len(got.Ignored) != 1 || got.Ignored[0].From != "src/a.ts" || got.Ignored[0].Specifier != "./generated/enums" || got.Ignored[0].Line != 1 {
		t.Errorf("unresolved = %s, ignored_imports = %+v; want ./generated/enums from src/a.ts:1 as ignored only", got.Unresolved, got.Ignored)
	}

	code, text, stderr := lupa(dir, "map", "main..feature")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(text, "- Imports of paths git ignores (likely generated): `./generated/enums` from `src/a.ts`") {
		t.Errorf("report lacks the ignored import line:\n%s", text)
	}
	if strings.Contains(text, "is a file missing from this change") {
		t.Errorf("an ignored import must not ask the author about a missing file:\n%s", text)
	}
}

func TestMapOwnersFromBaseAndGate(t *testing.T) {
	dir, _, _ := newRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".github/CODEOWNERS", "*.go @go-team\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "owners")
	gitCmd(t, dir, "switch", "-q", "feature")
	gitCmd(t, dir, "merge", "-q", "main")
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".github/CODEOWNERS", "*.go @go-team\nb.go @intruder\n")
	write(t, dir, "big.txt", strings.Repeat("line\n", 401))
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "change owners and grow")

	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Files []struct {
			Path   string   `json:"path"`
			Owners []string `json:"owners"`
		} `json:"files"`
		ReviewNow struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		} `json:"review_now"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	owners := map[string]string{}
	for _, f := range got.Files {
		owners[f.Path] = strings.Join(f.Owners, ",")
	}
	if owners["b.go"] != "@go-team" {
		t.Errorf("owners of b.go = %q, want @go-team from the base CODEOWNERS, not the change's", owners["b.go"])
	}
	if got.ReviewNow.Decision != "hand-back" || !strings.Contains(got.ReviewNow.Reason, "suggest a split") {
		t.Errorf("review_now = %+v, want hand-back suggesting a split", got.ReviewNow)
	}
}

func TestMapSaysWhichChangedLanguagesItDidNotAnalyze(t *testing.T) {
	dir, _, _ := newRepo(t)
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		NotAnalyzed []string `json:"not_analyzed"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.NotAnalyzed, ",") != ".go" {
		t.Errorf("not_analyzed = %v, want [.go]: b.go changed and only TS/JS is analyzed", got.NotAnalyzed)
	}
	write(t, dir, "x.txt", "")
	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "types.d.ts", "export type T = string;\n")
	gitCmd(t, dir, "add", "types.d.ts")
	gitCmd(t, dir, "commit", "-q", "-m", "types")
	gitCmd(t, dir, "switch", "-q", "main")
	_, stdout, _ = lupa(dir, "map", "main..feature", "--json")
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.NotAnalyzed, ",") != ".go" {
		t.Errorf("not_analyzed = %v, want [.go]: a .d.ts holds only types, TS is analyzed", got.NotAnalyzed)
	}
	_, text, _ := lupa(dir, "map", "main..feature")
	if !strings.Contains(text, "- Impact not analyzed for `.go` files: imports and call sites are followed for TS/JS only") {
		t.Errorf("report lacks the not-analyzed line:\n%s", text)
	}
}

func TestMapSinceLastLook(t *testing.T) {
	dir, _, firstHead := newRepo(t)
	sinceOf := func() (raw json.RawMessage) {
		t.Helper()
		code, stdout, stderr := lupa(dir, "map", "feature", "--json")
		if code != 0 {
			t.Fatalf("exit %d, stderr: %s", code, stderr)
		}
		var got struct {
			Since json.RawMessage `json:"since_last_look"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatal(err)
		}
		return got.Since
	}
	if got := string(sinceOf()); got != "null" {
		t.Errorf("first map since_last_look = %s, want null", got)
	}
	saved := filepath.Join(dir, ".git", "lupa", "feature", firstHead[:7], "signals.json")
	if _, err := os.Stat(saved); err != nil {
		t.Errorf("first map not saved at %s: %v", saved, err)
	}

	gitCmd(t, dir, "switch", "-q", "feature")
	write(t, dir, "c.txt", "new\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "more")
	var since struct {
		Since string   `json:"since"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(sinceOf(), &since); err != nil {
		t.Fatal(err)
	}
	if since.Since != firstHead || strings.Join(since.Files, ",") != "c.txt" {
		t.Errorf("since_last_look = %+v, want since %s with only c.txt", since, firstHead)
	}
}

func TestMapSinceLastLookIgnoresWhatARebaseBroughtFromTheBase(t *testing.T) {
	dir, _, _ := newRepo(t)
	if code, _, stderr := lupa(dir, "map", "feature", "--json"); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	write(t, dir, "z.txt", "from main\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "main moves on")
	gitCmd(t, dir, "switch", "-q", "feature")
	gitCmd(t, dir, "rebase", "-q", "main")
	write(t, dir, "c.txt", "new\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "more")
	gitCmd(t, dir, "switch", "-q", "main")

	code, stdout, stderr := lupa(dir, "map", "feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Since struct {
			Files []string `json:"files"`
		} `json:"since_last_look"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Since.Files, ",") != "c.txt" {
		t.Errorf("since_last_look files = %v, want only c.txt: z.txt came from the base", got.Since.Files)
	}
}

func TestMapListsAMovedFileOnceByItsNewPath(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.go", "package a\n\nvar A = 1\nvar B = 2\nvar C = 3\n")
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	gitCmd(t, dir, "mv", "a.go", "moved.go")
	gitCmd(t, dir, "commit", "-q", "-m", "move")
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var s struct {
		Files []struct {
			Path    string `json:"path"`
			OldPath string `json:"old_path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(s.Files) != 1 || s.Files[0].Path != "moved.go" || s.Files[0].OldPath != "a.go" {
		t.Errorf("files = %+v, want one entry moved.go from a.go", s.Files)
	}
}
