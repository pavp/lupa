package verify

import (
	"reflect"
	"testing"
)

func TestAddedLines(t *testing.T) {
	patch := "diff --git a/a.ts b/a.ts\n@@ -1,3 +1,4 @@\n keep\n-old\n+new\n+more\n keep\n@@ -10,2 +11,2 @@\n-x\n+y\n z\n"
	if got, want := AddedLines(patch), []int{2, 3, 11}; !reflect.DeepEqual(got, want) {
		t.Errorf("AddedLines() = %v, want %v", got, want)
	}
	if got := AddedLines(""); len(got) != 0 {
		t.Errorf("AddedLines(empty) = %v, want none", got)
	}
}
