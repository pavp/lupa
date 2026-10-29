package reach

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/lang/typescript"
)

// TestBuildOnResolutionTraps checks the approximate reach against the answer the compiler-backed analyzer
// produced on the same fixture: aliases with extends, barrels, re-exports, import type, dynamic import,
// require, directory index, .js to .ts, workspace packages, comment and string traps, an unresolved import.
func TestBuildOnResolutionTraps(t *testing.T) {
	root := "testdata/tsrepo"
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+"/"))
		if d.Type().IsRegular() && !strings.HasPrefix(rel, "node_modules/") {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(files, func(p string) ([]byte, error) { return os.ReadFile(filepath.Join(root, p)) }, typescript.Provider{})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("testdata/tsrepo.expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Importers  map[string][]string `json:"importers"`
		Imports    map[string][]string `json:"imports"`
		Unresolved []Unresolved        `json:"unresolved"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for file, importers := range want.Importers {
		got := g.Importers(file)
		if len(got) == 0 && len(importers) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, importers) {
			t.Errorf("importers of %s = %v, want %v", file, got, importers)
		}
	}
	for file, imports := range want.Imports {
		got := append([]string(nil), g.Imports[file]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, imports) {
			t.Errorf("imports of %s = %v, want %v", file, got, imports)
		}
	}
	if !reflect.DeepEqual(g.Unresolved, want.Unresolved) {
		t.Errorf("unresolved = %v, want %v", g.Unresolved, want.Unresolved)
	}
}
