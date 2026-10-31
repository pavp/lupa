package typescript

import (
	"errors"
	"testing"
)

func resolverOver(t *testing.T, contents map[string]string) *Resolver {
	t.Helper()
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
		t.Fatal(err)
	}
	return r
}

func TestWorkspacePackages(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]string
	}{
		{"npm workspaces", map[string]string{"package.json": `{ "workspaces": ["apps/*", "packages/*"] }`}},
		{"yarn workspaces object", map[string]string{"package.json": `{ "workspaces": { "packages": ["apps/*", "packages/*"] } }`}},
		{"pnpm workspace file", map[string]string{"package.json": `{}`, "pnpm-workspace.yaml": "packages:\n  - 'apps/*'\n  - \"packages/*\"\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			contents := map[string]string{
				"packages/ui/package.json":   `{ "name": "@acme/ui", "exports": { "./badge": "./src/Badge.tsx", "./icons/*": { "types": "./types/*.d.ts", "import": "./src/icons/*.ts" } } }`,
				"packages/ui/src/Badge.tsx":  "",
				"packages/ui/src/icons/x.ts": "",
				"packages/core/package.json": `{ "name": "@acme/core", "main": "./dist/index.js" }`,
				"packages/core/src/index.ts": "",
				"packages/core/index.ts":     "",
				"packages/core/util.ts":      "",
				"apps/web/src/Home.tsx":      "",
				"node_modules/@acme/ui":      "../../packages/ui",
			}
			for k, v := range c.manifest {
				contents[k] = v
			}
			r := resolverOver(t, contents)
			for _, tc := range []struct {
				spec, want string
				status     Status
			}{
				{"@acme/ui/badge", "packages/ui/src/Badge.tsx", Resolved},
				{"@acme/ui/icons/x", "packages/ui/src/icons/x.ts", Resolved},
				{"@acme/ui/missing", "", Unresolved},
				{"@acme/core", "packages/core/index.ts", Resolved},
				{"@acme/core/util", "packages/core/util.ts", Resolved},
				{"@acme/core-extra", "", External},
				{"react", "", External},
			} {
				got, status := r.Resolve("apps/web/src/Home.tsx", tc.spec)
				if got != tc.want || status != tc.status {
					t.Errorf("Resolve(%q) = %q, %v; want %q, %v", tc.spec, got, status, tc.want, tc.status)
				}
			}
		})
	}
}

func TestAGeneratedModuleGitIgnoresIsExternal(t *testing.T) {
	r := resolverOver(t, map[string]string{
		"package.json":                  `{ "workspaces": ["packages/*"] }`,
		".gitignore":                    "# generated\npackages/prisma/enums\npackages/prisma/zod/*.ts\n!packages/prisma/zod/keep.ts\n",
		"packages/prisma/package.json":  `{ "name": "@acme/prisma" }`,
		"packages/prisma/index.ts":      "",
		"packages/prisma/.gitignore":    "client\n",
		"packages/prisma/lib/client.ts": "",
		"apps/web/src/Home.tsx":         "",
	})
	for _, tc := range []struct {
		spec   string
		status Status
	}{
		{"@acme/prisma/enums", External},
		{"@acme/prisma/zod/booking", External},
		{"@acme/prisma/client", External},
		{"@acme/prisma/zod/keep", Unresolved},
		{"@acme/prisma/missing", Unresolved},
		{"../../../packages/prisma/enums", External},
	} {
		if _, status := r.Resolve("apps/web/src/Home.tsx", tc.spec); status != tc.status {
			t.Errorf("Resolve(%q) status = %v; want %v", tc.spec, status, tc.status)
		}
	}
}
