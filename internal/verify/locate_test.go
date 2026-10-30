package verify

import (
	"strings"
	"testing"
)

func lines(ls ...string) string {
	return strings.Join(ls, "\n") + "\n"
}

var headFiles = map[string]string{
	"src/a.go": lines(
		"package a",
		"",
		`import "fmt"`,
		"",
		"func Hello(name string) string {",
		`	if name == "" {`,
		`		name = "world"`,
		"	}",
		"",
		"	retried := false",
		`	return fmt.Sprintf("hello %s", name)`,
		"}",
		"",
		"func Unchanged() int {",
		"	return 42",
		"}",
		"",
		"func Helper() string {",
		`	return "help"`,
		"}",
		"",
		"func Other() bool {",
		"	return true",
		"}",
		"",
		"func Pad() {",
		`	fmt.Println("pad")`,
		"}",
		"",
		"func Sum(xs []int) int {",
		"	total := 0",
		"	retried := false",
		"	for _, x := range xs {",
		"		total += x   ",
		"	}",
		"	return total",
		"}",
	),
	"src/b.go": lines(
		"package b",
		"",
		"func New() *Thing {",
		"	return newThing()",
		"}",
	),
	"src/c.go": lines("package c", "", "func C() {", `	log.Println("dup")`, "}"),
	"src/d.go": lines("package d", "", "func D() {", `	log.Println("dup")`, "}"),
}

var patches = map[string]string{
	"src/a.go": lines(
		"diff --git a/src/a.go b/src/a.go",
		"index 6996083..84753fa 100644",
		"--- a/src/a.go",
		"+++ b/src/a.go",
		"@@ -3,7 +3,12 @@ package a",
		` import "fmt"`,
		" ",
		" func Hello(name string) string {",
		`-	return "hello " + name`,
		`+	if name == "" {`,
		`+		name = "world"`,
		"+	}",
		"+",
		"+	retried := false",
		`+	return fmt.Sprintf("hello %s", name)`,
		" }",
		" ",
		" func Unchanged() int {",
		"@@ -24,8 +29,9 @@ func Pad() {",
		" ",
		" func Sum(xs []int) int {",
		" 	total := 0",
		"+	retried := false",
		" 	for _, x := range xs {",
		"-		total = total + x",
		"+		total += x   ",
		" 	}",
		" 	return total",
		" }",
	),
	"src/b.go": lines(
		"diff --git a/src/b.go b/src/b.go",
		"new file mode 100644",
		"index 0000000..9c746b2",
		"--- /dev/null",
		"+++ b/src/b.go",
		"@@ -0,0 +1,5 @@",
		"+package b",
		"+",
		"+func New() *Thing {",
		"+	return newThing()",
		"+}",
	),
	"src/c.go": lines(
		"diff --git a/src/c.go b/src/c.go",
		"index 1428ae9..c1564ee 100644",
		"--- a/src/c.go",
		"+++ b/src/c.go",
		"@@ -1,4 +1,5 @@",
		" package c",
		" ",
		" func C() {",
		`+	log.Println("dup")`,
		" }",
	),
	"src/d.go": lines(
		"diff --git a/src/d.go b/src/d.go",
		"index b8a6efa..63eef55 100644",
		"--- a/src/d.go",
		"+++ b/src/d.go",
		"@@ -1,4 +1,5 @@",
		" package d",
		" ",
		" func D() {",
		`+	log.Println("dup")`,
		" }",
	),
	"src/old.go": lines(
		"diff --git a/src/old.go b/src/old.go",
		"deleted file mode 100644",
		"index 00f0db6..0000000",
		"--- a/src/old.go",
		"+++ /dev/null",
		"@@ -1,5 +0,0 @@",
		"-package old",
		"-",
		"-func Gone() {",
		"-	cleanup()",
		"-}",
	),
	"img/logo.png": lines(
		"diff --git a/img/logo.png b/img/logo.png",
		"index 1111111..2222222 100644",
		"Binary files a/img/logo.png and b/img/logo.png differ",
	),
	"empty.txt": "",
}

func headOf(path string) (string, bool) {
	s, ok := headFiles[path]
	return s, ok
}

func TestLocateFindsQuotedCode(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		quote string
		want  Location
	}{
		{
			name:  "single added line",
			file:  "src/a.go",
			quote: `		name = "world"`,
			want:  Location{File: "src/a.go", Side: "new", Start: 7, End: 7, Method: "new-side"},
		},
		{
			name:  "multi-line added block",
			file:  "src/a.go",
			quote: "\tif name == \"\" {\n\t\tname = \"world\"\n\t}",
			want:  Location{File: "src/a.go", Side: "new", Start: 6, End: 8, Method: "new-side"},
		},
		{
			name:  "added line in the second hunk numbered by head line",
			file:  "src/a.go",
			quote: "total += x",
			want:  Location{File: "src/a.go", Side: "new", Start: 34, End: 34, Method: "new-side"},
		},
		{
			name:  "indentation and trailing spaces differ",
			file:  "src/a.go",
			quote: "    if name == \"\" {   \n  name = \"world\"\t",
			want:  Location{File: "src/a.go", Side: "new", Start: 6, End: 7, Method: "new-side"},
		},
		{
			name:  "diff markers pasted in the quote",
			file:  "src/a.go",
			quote: "+\tif name == \"\" {\n+\t\tname = \"world\"\n+\t}",
			want:  Location{File: "src/a.go", Side: "new", Start: 6, End: 8, Method: "new-side"},
		},
		{
			name:  "diff markers mixing context and added lines",
			file:  "src/a.go",
			quote: " \ttotal := 0\n+\tretried := false\n \tfor _, x := range xs {",
			want:  Location{File: "src/a.go", Side: "new", Start: 31, End: 33, Method: "new-side"},
		},
		{
			name:  "blank code line inside the quoted range is skipped",
			file:  "src/a.go",
			quote: "\t}\n\tretried := false",
			want:  Location{File: "src/a.go", Side: "new", Start: 8, End: 10, Method: "new-side"},
		},
		{
			name:  "blank quote line is skipped",
			file:  "src/a.go",
			quote: "func Sum(xs []int) int {\n\n\n\ttotal := 0",
			want:  Location{File: "src/a.go", Side: "new", Start: 30, End: 31, Method: "new-side"},
		},
		{
			name:  "removed line found on the old side",
			file:  "src/a.go",
			quote: `	return "hello " + name`,
			want:  Location{File: "src/a.go", Side: "old", Start: 6, End: 6, Method: "old-side"},
		},
		{
			name:  "context and removed lines numbered by base line",
			file:  "src/a.go",
			quote: "for _, x := range xs {\n\ttotal = total + x",
			want:  Location{File: "src/a.go", Side: "old", Start: 27, End: 28, Method: "old-side"},
		},
		{
			name:  "unchanged code outside the hunks found in the full file",
			file:  "src/a.go",
			quote: "func Helper() string {\n\treturn \"help\"\n}",
			want:  Location{File: "src/a.go", Side: "new", Start: 18, End: 20, Method: "full-file"},
		},
		{
			name:  "quote of another changed file is re-filed",
			file:  "src/a.go",
			quote: "return newThing()",
			want:  Location{File: "src/b.go", Side: "new", Start: 4, End: 4, Method: "other-file"},
		},
		{
			name:  "duplicate in the same file takes the first and is ambiguous",
			file:  "src/a.go",
			quote: "retried := false",
			want:  Location{File: "src/a.go", Side: "new", Start: 10, End: 10, Method: "new-side", Ambiguous: true},
		},
		{
			name:  "new file has its added lines on the new side",
			file:  "src/b.go",
			quote: "func New() *Thing {\n\treturn newThing()",
			want:  Location{File: "src/b.go", Side: "new", Start: 3, End: 4, Method: "new-side"},
		},
		{
			name:  "deleted file is found on the old side",
			file:  "src/old.go",
			quote: "func Gone() {\n\tcleanup()",
			want:  Location{File: "src/old.go", Side: "old", Start: 3, End: 4, Method: "old-side"},
		},
		{
			name:  "comment on a file outside the change is re-filed",
			file:  "src/unknown.go",
			quote: "return newThing()",
			want:  Location{File: "src/b.go", Side: "new", Start: 4, End: 4, Method: "other-file"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Locate(tt.quote, tt.file, patches, headOf)
			if !ok {
				t.Fatalf("Locate(%q, %q) missed, want %+v", tt.quote, tt.file, tt.want)
			}
			if got != tt.want {
				t.Errorf("Locate(%q, %q) = %+v, want %+v", tt.quote, tt.file, got, tt.want)
			}
		})
	}
}

func TestLocateMisses(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		quote string
	}{
		{"lines not consecutive in the code", "src/a.go", "if name == \"\" {\nretried := false"},
		{"half a line", "src/a.go", `name = "wor`},
		{"a line plus a prefix of the next", "src/a.go", "total := 0\nretried"},
		{"case differs", "src/a.go", `IF NAME == "" {`},
		{"empty quote", "src/a.go", ""},
		{"whitespace-only quote", "src/a.go", "  \n\t\n   "},
		{"only diff markers", "src/a.go", "+\n+\n-"},
		{"quote found in two other changed files", "src/a.go", `log.Println("dup")`},
		{"code that exists nowhere", "src/a.go", "return 43"},
		{"deleted file has no head content", "src/old.go", "return 43"},
		{"deleted code is not other-file material", "src/b.go", "cleanup()"},
		{"binary patch", "img/logo.png", "Binary files a/img/logo.png and b/img/logo.png differ"},
		{"empty patch", "empty.txt", "package a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := Locate(tt.quote, tt.file, patches, headOf); ok {
				t.Errorf("Locate(%q, %q) = %+v, want a miss", tt.quote, tt.file, got)
			}
		})
	}
}

func TestLocateAtFindsTheCitedOccurrence(t *testing.T) {
	content := "func a() {\n\treturn nil\n}\n\nfunc b() {\n\treturn nil\n}\n"
	for _, c := range []struct {
		line       int
		want       bool
		start, end int
	}{{2, true, 2, 2}, {6, true, 6, 6}, {3, false, 0, 0}} {
		loc, ok := LocateAt("return nil", content, c.line)
		if ok != c.want || (ok && (loc.Start != c.start || loc.End != c.end)) {
			t.Errorf("LocateAt(line %d) = %+v, %v; want %v at %d-%d", c.line, loc, ok, c.want, c.start, c.end)
		}
	}
	if _, ok := LocateAt("   ", content, 2); ok {
		t.Error("a blank quote was located")
	}
}
