package typescript

import (
	"errors"
	"testing"
)

func newTestResolver(t *testing.T) *Resolver {
	t.Helper()
	contents := map[string]string{
		"tsconfig.base.json": `{
			// shared options
			"compilerOptions": {
				"baseUrl": ".",
				"paths": { "@/*": ["apps/web/src/*"], "@config": ["config/index.ts"], },
			},
		}`,
		"tsconfig.json":                 `{ "extends": "./tsconfig.base.json", "include": ["apps"] }`,
		"packages/kit/tsconfig.json":    `{ "compilerOptions": { "paths": { "~/*": ["./src/*"] } } }`,
		"packages/kit/src/button.ts":    "",
		"packages/kit/src/index.ts":     "",
		"apps/web/src/lib/date.ts":      "",
		"apps/web/src/lib/index.ts":     "",
		"apps/web/src/components/A.tsx": "",
		"apps/web/src/types.d.ts":       "",
		"apps/web/src/legacy/old.js":    "",
		"apps/web/src/pages/Home.tsx":   "",
		"config/index.ts":               "",
	}
	files := make([]string, 0, len(contents))
	for f := range contents {
		files = append(files, f)
	}
	r, err := NewResolver(files, func(p string) ([]byte, error) {
		c, ok := contents[p]
		if !ok {
			return nil, errors.New("not found")
		}
		return []byte(c), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolve(t *testing.T) {
	r := newTestResolver(t)
	cases := []struct {
		name, from, spec string
		want             string
		status           Status
	}{
		{"relative without extension", "apps/web/src/pages/Home.tsx", "../lib/date", "apps/web/src/lib/date.ts", Resolved},
		{"js extension maps to ts", "apps/web/src/pages/Home.tsx", "../lib/date.js", "apps/web/src/lib/date.ts", Resolved},
		{"directory index", "apps/web/src/pages/Home.tsx", "../lib", "apps/web/src/lib/index.ts", Resolved},
		{"tsx file", "apps/web/src/pages/Home.tsx", "../components/A", "apps/web/src/components/A.tsx", Resolved},
		{"javascript file", "apps/web/src/pages/Home.tsx", "../legacy/old", "apps/web/src/legacy/old.js", Resolved},
		{"alias from an extended config", "apps/web/src/pages/Home.tsx", "@/lib/date", "apps/web/src/lib/date.ts", Resolved},
		{"exact alias", "apps/web/src/pages/Home.tsx", "@config", "config/index.ts", Resolved},
		{"nearest tsconfig wins", "packages/kit/src/index.ts", "~/button", "packages/kit/src/button.ts", Resolved},
		// A declaration file is types, not code: no graph edge, but the import is not missing either.
		{"declaration files are not code", "apps/web/src/pages/Home.tsx", "../types", "", External},
		{"missing relative file", "apps/web/src/pages/Home.tsx", "./missing", "", Unresolved},
		{"missing alias target", "apps/web/src/pages/Home.tsx", "@/nope", "", Unresolved},
		{"package outside the repo", "apps/web/src/pages/Home.tsx", "react", "", External},
		{"node builtin", "apps/web/src/pages/Home.tsx", "node:path", "", External},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, status := r.Resolve(c.from, c.spec)
			if got != c.want || status != c.status {
				t.Errorf("Resolve(%q, %q) = %q, %v; want %q, %v", c.from, c.spec, got, status, c.want, c.status)
			}
		})
	}
}

func TestNewResolverRejectsBrokenConfig(t *testing.T) {
	_, err := NewResolver([]string{"tsconfig.json"}, func(string) ([]byte, error) { return []byte(`{ "compilerOptions": `), nil })
	if err == nil {
		t.Fatal("want an error naming the broken tsconfig.json")
	}
}

func TestTsconfigWinsOverJsconfig(t *testing.T) {
	contents := map[string]string{
		"jsconfig.json": `{ "compilerOptions": { "paths": { "#/*": ["js/*"] } } }`,
		"tsconfig.json": `{ "compilerOptions": { "paths": { "#/*": ["ts/*"] } } }`,
		"js/a.ts":       "",
		"ts/a.ts":       "",
		"main.ts":       "",
	}
	files := []string{"jsconfig.json", "tsconfig.json", "js/a.ts", "ts/a.ts", "main.ts"}
	r, err := NewResolver(files, func(p string) ([]byte, error) { return []byte(contents[p]), nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Resolve("main.ts", "#/a"); got != "ts/a.ts" {
		t.Errorf("Resolve = %q, want ts/a.ts from tsconfig.json", got)
	}
}

func TestExtendsList(t *testing.T) {
	contents := map[string]string{
		"tsconfig.strict.json": `{ "compilerOptions": { "paths": { "@/*": ["old/*"] } } }`,
		"tsconfig.paths.json":  `{ "compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } } }`,
		"app/tsconfig.json":    `{ "extends": ["../tsconfig.strict.json", "../tsconfig.paths.json", "@tsconfig/node20"] }`,
		"src/lib/date.ts":      "",
		"old/lib/date.ts":      "",
		"app/main.ts":          "",
	}
	files := make([]string, 0, len(contents))
	for f := range contents {
		files = append(files, f)
	}
	r, err := NewResolver(files, func(p string) ([]byte, error) {
		if c, ok := contents[p]; ok {
			return []byte(c), nil
		}
		return nil, errors.New("not found")
	})
	if err != nil {
		t.Fatalf("an extends list: %v", err)
	}
	if got, status := r.Resolve("app/main.ts", "@/lib/date"); got != "src/lib/date.ts" || status != Resolved {
		t.Errorf("Resolve = %q, %v; want src/lib/date.ts from the last config of the list", got, status)
	}
}

func TestExtendsAGeneratedConfigThatIsNotInTheRepo(t *testing.T) {
	contents := map[string]string{
		"web/tsconfig.json": `{ "extends": "./.svelte-kit/tsconfig.json", "compilerOptions": { "baseUrl": ".", "paths": { "$lib/*": ["src/lib/*"] } } }`,
		"web/src/lib/a.ts":  "",
		"web/src/main.ts":   "",
	}
	files := make([]string, 0, len(contents))
	for f := range contents {
		files = append(files, f)
	}
	r, err := NewResolver(files, func(p string) ([]byte, error) {
		if c, ok := contents[p]; ok {
			return []byte(c), nil
		}
		return nil, errors.New(p + " missing")
	})
	if err != nil {
		t.Fatalf("a config extending a generated file: %v", err)
	}
	if got, status := r.Resolve("web/src/main.ts", "$lib/a"); got != "web/src/lib/a.ts" || status != Resolved {
		t.Errorf("Resolve = %q, %v; want the config's own paths to still apply", got, status)
	}
}
