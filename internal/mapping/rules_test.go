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
		{"packages/features/bookings/webhook.e2e.ts", Test, Verification},
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
		{"packages/features/form-builder/schema.ts", Skim, NoRisk},
		{"db/schema.sql", ReadFirst, OneWayDoor},
		{".github/PULL_REQUEST_TEMPLATE.md", Skim, NoRisk},
		{".github/ISSUE_TEMPLATE/bug.yml", Skim, NoRisk},
		{".github/actions/setup/action.yml", ReadFirst, Impact},
		{"packages/trpc/deleteCredential.handler.ts", ReadFirst, Impact},
		{"src/lib/passwordReset.ts", ReadFirst, Impact},
		{"src/jwt.ts", ReadFirst, Impact},
		{"src/secrets/store.ts", ReadFirst, Impact},
		{"vendor/github.com/x/y.go", Skip, NoRisk},
		{"bun.lock", Skip, NoRisk},
		{"api/service_pb2_grpc.py", Skip, NoRisk},
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
	got := Build(Input{Base: "b", Head: "h", Files: []File{
		{Path: "src/app.ts", Additions: 10, Deletions: 2},
		{Path: "package-lock.json", Additions: 900, Deletions: 300},
	}})
	want := Size{Files: 1, Additions: 10, Deletions: 2, Changed: 12, GeneratedFiles: 1}
	if got.Size != want {
		t.Errorf("size = %+v, want %+v", got.Size, want)
	}
	if got.Files[1].Class != Skip || got.Files[0].Class != Skim {
		t.Errorf("classes = %q %q, want skim skip", got.Files[0].Class, got.Files[1].Class)
	}
}

func TestBuildPromotesFilesWithOutsideImporters(t *testing.T) {
	got := Build(Input{Base: "b", Head: "h", Files: []File{
		{Path: "src/Badge.tsx", Additions: 3},
		{Path: "src/auth/session.ts", Additions: 1},
		{Path: "src/lonely.ts", Additions: 1},
	}, Reach: Reach{
		Importers: map[string][]string{
			"src/Badge.tsx":       {"src/a.tsx", "src/b.tsx"},
			"src/auth/session.ts": {"src/app.ts"},
		},
		Unresolved: []Unresolved{{From: "src/Badge.tsx", Specifier: "./missing"}},
	}})
	badge, session, lonely := got.Files[0], got.Files[1], got.Files[2]
	if badge.Class != ReadFirst || badge.Risk != Impact || badge.Reason != "imported by 2 files outside this change" || len(badge.Importers) != 2 {
		t.Errorf("badge = %+v, want read-first impact with 2 importers", badge)
	}
	if session.Reason != "auth, permissions, sessions, tokens or secrets by name" || len(session.Importers) != 1 {
		t.Errorf("session = %+v, want its auth reason kept and 1 importer listed", session)
	}
	if lonely.Class != Skim || lonely.Importers != nil {
		t.Errorf("lonely = %+v, want skim without importers", lonely)
	}
	if len(got.Unresolved) != 1 || got.Unresolved[0].Specifier != "./missing" {
		t.Errorf("unresolved = %+v", got.Unresolved)
	}
}
