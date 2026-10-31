package mapping

import (
	"reflect"
	"testing"
)

func TestBuild(t *testing.T) {
	cases := []struct {
		name  string
		files []File
		want  Size
	}{
		{"no changes", nil, Size{}},
		{"binary file counts as a file without lines", []File{{Path: "logo.png", Binary: true}, {Path: "a.go", Additions: 3, Deletions: 1}}, Size{Files: 2, Additions: 3, Deletions: 1, Changed: 4}},
		{"whitespace-only lines are not changed lines", []File{{Path: "a.go", Additions: 30, Deletions: 30, WhitespaceOnly: 50}}, Size{Files: 1, Additions: 30, Deletions: 30, Changed: 10}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Build(Input{Base: "b", Head: "h", Files: c.files})
			if got.Schema != Schema || got.Base != "b" || got.Head != "h" {
				t.Errorf("header = %q %q %q", got.Schema, got.Base, got.Head)
			}
			if got.Files == nil {
				t.Error("files is nil; JSON must carry an empty list, not null")
			}
			if !reflect.DeepEqual(got.Size, c.want) {
				t.Errorf("size = %+v, want %+v", got.Size, c.want)
			}
		})
	}
}

func TestImpactByCallSites(t *testing.T) {
	files := []File{{Path: "src/a.ts", Additions: 1}, {Path: "src/b.ts", Additions: 1}, {Path: "src/c.go", Additions: 1}}
	reach := Reach{
		Importers: map[string][]string{"src/a.ts": {"x.ts", "y.ts"}, "src/b.ts": {"z.ts"}, "src/c.go": {"d.go"}},
		// a.ts's changed declarations have no caller outside the change; b.ts's have one; c.go is not analyzed.
		Calls: map[string][]string{"src/a.ts": nil, "src/b.ts": {"z.ts:3"}},
	}
	got := map[string]File{}
	for _, f := range Build(Input{Files: files, Reach: reach}).Files {
		got[f.Path] = f
	}
	if a := got["src/a.ts"]; a.Risk == Impact {
		t.Errorf("a.ts = %+v, want no impact: importers reach the file but no caller of what changed", a)
	}
	if b := got["src/b.ts"]; b.Risk != Impact || b.Class != ReadFirst || b.Reason != "1 call site of its changed declarations outside this change" || !reflect.DeepEqual(b.CallSites, []string{"z.ts:3"}) {
		t.Errorf("b.ts = %+v, want read first by its call site", b)
	}
	if c := got["src/c.go"]; c.Risk != Impact || c.Reason != "imported by 1 file outside this change" {
		t.Errorf("c.go = %+v, want impact by importers where calls are not analyzed", c)
	}
}

func TestReadFirstWhenOtherChangedFilesCallIt(t *testing.T) {
	files := []File{{Path: "src/lib.ts", Additions: 1}, {Path: "src/a.ts", Additions: 1}, {Path: "src/b.ts", Additions: 1}}
	reach := Reach{Calls: map[string][]string{"src/lib.ts": nil}, ChangedCallers: map[string][]string{"src/lib.ts": {"src/a.ts", "src/b.ts"}}}
	for _, f := range Build(Input{Files: files, Reach: reach}).Files {
		if f.Path == "src/lib.ts" && (f.Class != ReadFirst || f.Reason != "its changed declarations are called from 2 other changed files") {
			t.Errorf("lib.ts = %+v, want read first: the rest of the change depends on it", f)
		}
	}
}
