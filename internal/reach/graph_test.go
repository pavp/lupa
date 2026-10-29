package reach

import (
	"slices"
	"testing"
)

func TestDirectImporters(t *testing.T) {
	g := Graph{Imports: map[string][]string{
		"src/a.ts": {"src/x.ts"},
		"src/b.ts": {"src/a.ts"},
		"src/c.ts": {"src/y.ts", "src/x.ts"},
	}}
	if got := g.DirectImporters("src/x.ts"); !slices.Equal(got, []string{"src/a.ts", "src/c.ts"}) {
		t.Errorf("direct importers = %v, want a and c, not b which reaches x through a", got)
	}
	if got := g.Importers("src/x.ts"); !slices.Equal(got, []string{"src/a.ts", "src/b.ts", "src/c.ts"}) {
		t.Errorf("importers = %v, want every file that reaches x", got)
	}
}
