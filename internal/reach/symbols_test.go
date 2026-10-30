package reach

import (
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"testing"

	"github.com/pavp/lupa/internal/lang/typescript"
)

func TestChangedSymbols(t *testing.T) {
	repo := map[string]string{
		"src/util.ts":      "export function slug(s) {\n  return s.trim();\n}\nexport const other = 1;\n",
		"src/index.ts":     "export * from './load';\nexport * from './util';\n",
		"src/load.ts":      "import { slug } from './util';\n\nexport function load(id) {\n  return slug(id);\n}\n\nexport function keep() {\n  return load(1);\n}\n",
		"src/page.ts":      "import { load as fetchIt } from './index';\n\nfetchIt(2);\n",
		"src/elsewhere.ts": "export function load() {}\n",
		"src/other.ts":     "import { load } from './elsewhere';\nimport './load';\nload(3);\n",
		"src/handler.ts":   "import { slug } from './index';\n\nexport default function handler() {\n  return slug('x');\n}\n",
		"src/app.ts":       "import h from './handler';\nh();\n",
	}
	files := make([]string, 0, len(repo))
	for f := range repo {
		files = append(files, f)
	}
	slices.Sort(files)
	read := func(p string) ([]byte, error) {
		if s, ok := repo[p]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
	p := typescript.Provider{}
	g, err := Build(files, read, p)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := p.NewResolver(files, read)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ChangedSymbols(SymbolInput{
		Graph: g, ReadFile: read, Symbols: p, Resolver: resolver,
		Added: map[string][]int{"src/load.ts": {1, 4}, "src/handler.ts": {4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Symbol{
		{File: "src/handler.ts", Name: "handler", Line: 3, End: 5,
			Callers: []Site{{File: "src/app.ts", Line: 2}},
			Callees: []Callee{{Name: "slug", Site: Site{File: "src/util.ts", Line: 1}}}},
		{File: "src/load.ts", Name: "load", Line: 3, End: 5,
			Callers: []Site{{File: "src/load.ts", Line: 8}, {File: "src/page.ts", Line: 3}},
			Callees: []Callee{{Name: "slug", Site: Site{File: "src/util.ts", Line: 1}}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedSymbols() =\n%+v\nwant\n%+v", got, want)
	}

	if _, err := ChangedSymbols(SymbolInput{Graph: g, Symbols: p, Resolver: resolver, Added: map[string][]int{"src/gone.ts": {1}},
		ReadFile: func(string) ([]byte, error) { return nil, fs.ErrNotExist }}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unreadable changed file: err = %v, want fs.ErrNotExist", err)
	}
}
