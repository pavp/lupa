package mapping

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		path      string
		wantClass Class
		wantRisk  Risk
	}{
		{"db/migrations/0042_add_attempts.sql", ReadFirst, OneWayDoor},
		{"prisma/schema.prisma", ReadFirst, OneWayDoor},
		{"queries/report.sql", ReadFirst, OneWayDoor},
		{".github/workflows/ci.yml", ReadFirst, Impact},
		{"Dockerfile", ReadFirst, Impact},
		{"infra/terraform/main.tf", ReadFirst, Impact},
		{"src/auth/session.ts", ReadFirst, Impact},
		{"src/AuthService.ts", ReadFirst, Impact},
		{"src/permissions.go", ReadFirst, Impact},
		{"api/tokens/refresh.ts", ReadFirst, Impact},
		{".env.production", ReadFirst, Impact},
		{"package.json", ReadFirst, Impact},
		{"services/api/go.mod", ReadFirst, Impact},
		{"requirements-dev.txt", ReadFirst, Impact},
		{"src/user.test.ts", Test, Verification},
		{"src/user.spec.tsx", Test, Verification},
		{"internal/cli/cli_test.go", Test, Verification},
		{"tests/test_user.py", Test, Verification},
		{"src/__tests__/user.ts", Test, Verification},
		{"package-lock.json", Skip, NoRisk},
		{"web/pnpm-lock.yaml", Skip, NoRisk},
		{"go.sum", Skip, NoRisk},
		{"src/__snapshots__/Badge.test.tsx.snap", Skip, NoRisk},
		{"dist/auth.js", Skip, NoRisk},
		{"api/user.pb.go", Skip, NoRisk},
		{"db/migrations/generated/002.sql", Skip, NoRisk},
		{"src/author.ts", Skim, NoRisk},
		{"src/tokenizer.ts", Skim, NoRisk},
		{"README.md", Skim, NoRisk},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			got := Classify(c.path)
			if got.Class != c.wantClass || got.Risk != c.wantRisk || got.Reason == "" {
				t.Errorf("Classify(%q) = %+v, want class %q risk %q and a reason", c.path, got, c.wantClass, c.wantRisk)
			}
		})
	}
}

func TestBuildExcludesGeneratedFromSize(t *testing.T) {
	got := Build("b", "h", []File{
		{Path: "src/app.ts", Additions: 10, Deletions: 2},
		{Path: "package-lock.json", Additions: 900, Deletions: 300},
	})
	want := Size{Files: 1, Additions: 10, Deletions: 2, Changed: 12, GeneratedFiles: 1}
	if got.Size != want {
		t.Errorf("size = %+v, want %+v", got.Size, want)
	}
	if got.Files[1].Class != Skip || got.Files[0].Class != Skim {
		t.Errorf("classes = %q %q, want skim skip", got.Files[0].Class, got.Files[1].Class)
	}
}
