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
