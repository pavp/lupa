package owners

import (
	"strings"
	"testing"
)

const codeowners = `# Global owners
*                 @org/everyone
*.js              @js-owner
/build/logs/      @doctocat
docs/*            docs@example.com
apps/             @octocat
/scripts/**/run.sh @runner
/vendor/          # no owners: explicitly unowned
README.md         @readme-a @readme-b
`

func TestOwners(t *testing.T) {
	f := Parse([]byte(codeowners))
	cases := []struct {
		path string
		want string
	}{
		{"src/main.go", "@org/everyone"},
		{"web/app.js", "@js-owner"},
		{"build/logs/today.log", "@doctocat"},
		{"build/logs/old/a.log", "@doctocat"},
		{"src/build/logs/x.log", "@org/everyone"},
		{"docs/getting-started.md", "docs@example.com"},
		{"docs/build-app/troubleshooting.md", "@org/everyone"},
		{"apps/web/index.ts", "@octocat"},
		{"services/apps/x.ts", "@octocat"},
		{"scripts/a/b/run.sh", "@runner"},
		{"scripts/run.sh", "@runner"},
		{"vendor/lib.go", ""},
		{"README.md", "@readme-a @readme-b"},
		{"pkg/README.md", "@readme-a @readme-b"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := strings.Join(f.Owners(c.path), " "); got != c.want {
				t.Errorf("Owners(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

func TestEmptyFileOwnsNothing(t *testing.T) {
	if got := Parse(nil).Owners("any.go"); got != nil {
		t.Errorf("Owners = %v, want nil", got)
	}
}
