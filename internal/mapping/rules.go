package mapping

import (
	"path"
	"strings"
	"unicode"
)

// Class is where a file sits in the reading order.
type Class string

const (
	ReadFirst Class = "read-first"
	Test      Class = "test"
	Skim      Class = "skim"
	Skip      Class = "skip"
)

// Risk is the risk category a rule attaches to a file; there is no numeric score.
type Risk string

const (
	OneWayDoor   Risk = "one-way-door"
	Impact       Risk = "impact"
	Verification Risk = "verification"
	NoRisk       Risk = "none"
)

// Rule is the outcome of classifying one path.
type Rule struct {
	Class  Class
	Risk   Risk
	Reason string
}

var (
	lockfiles = set("package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock", "go.sum",
		"cargo.lock", "poetry.lock", "pipfile.lock", "uv.lock", "gemfile.lock", "composer.lock")
	manifests = set("package.json", "go.mod", "pyproject.toml", "pipfile", "setup.py", "cargo.toml", "gemfile",
		"composer.json", "pom.xml", "build.gradle", "build.gradle.kts")
	ciFiles   = set(".gitlab-ci.yml", "jenkinsfile", "azure-pipelines.yml", "bitbucket-pipelines.yml")
	ciDirs    = set(".circleci", "terraform", "k8s", "kubernetes", "helm", "infra", "deploy")
	authWords = set("auth", "oauth", "authorization", "permission", "permissions", "session", "sessions", "token", "tokens",
		"credential", "credentials", "password", "passwords", "secret", "secrets", "jwt", "login")
	// schemaFiles are database schemas; a `schema.ts` is usually a validation schema, which rolls back like any code.
	schemaFiles = set("schema.prisma", "schema.rb", "structure.sql")
	testDirs    = set("test", "tests", "__tests__", "spec")
	migrateDirs = set("migrations", "migration", "migrate")
)

// Classify applies the universal path rules; the first match wins. Generated output matches first:
// it is never authored code, even under an auth or migration path.
func Classify(p string) Rule {
	base := strings.ToLower(path.Base(p))
	dirs := dirSegments(p)
	switch {
	case lockfiles[base]:
		return Rule{Skip, NoRisk, "lockfile"}
	case dirs["__snapshots__"] || strings.HasSuffix(base, ".snap"):
		return Rule{Skip, NoRisk, "snapshot"}
	case dirs["dist"]:
		return Rule{Skip, NoRisk, "build output"}
	case dirs["generated"]:
		return Rule{Skip, NoRisk, "generated code"}
	case dirs["vendor"]:
		return Rule{Skip, NoRisk, "vendored code"}
	case isGenerated(base):
		return Rule{Skip, NoRisk, "generated code"}
	case anyOf(dirs, migrateDirs) || strings.HasSuffix(base, ".sql") || strings.HasSuffix(base, ".prisma") || schemaFiles[base]:
		return Rule{ReadFirst, OneWayDoor, "migration or schema: hard to roll back"}
	case anyOf(dirs, ciDirs) || isGitHubCI(p) || ciFiles[base] || strings.HasPrefix(base, "dockerfile") ||
		strings.HasPrefix(base, "docker-compose") || strings.HasSuffix(base, ".tf"):
		return Rule{ReadFirst, Impact, "CI or infrastructure"}
	case strings.HasPrefix(base, ".env") || hasAuthWord(p):
		return Rule{ReadFirst, Impact, "auth, permissions, sessions, tokens or secrets by name"}
	case manifests[base] || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return Rule{ReadFirst, Impact, "dependency manifest"}
	case isTest(base, dirs):
		return Rule{Test, Verification, "test: read before the code it tests"}
	default:
		return Rule{Skim, NoRisk, "no rule matched"}
	}
}

func isGenerated(base string) bool {
	for _, marker := range []string{".generated.", ".gen.", ".min.js", ".min.css"} {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return strings.HasSuffix(base, ".pb.go") || strings.HasSuffix(base, "_pb2.py") || strings.HasSuffix(base, "_pb2_grpc.py") ||
		strings.HasSuffix(base, "_pb2.pyi")
}

// isGitHubCI keeps .github's workflows, actions and dependabot config; its issue and PR templates are prose.
func isGitHubCI(p string) bool {
	p = strings.ToLower(p)
	return strings.HasPrefix(p, ".github/workflows/") || strings.HasPrefix(p, ".github/actions/") || p == ".github/dependabot.yml"
}

func isTest(base string, dirs map[string]bool) bool {
	return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains(base, ".e2e.") ||
		strings.Contains(base, "_test.") || strings.HasPrefix(base, "test_") || anyOf(dirs, testDirs)
}

// hasAuthWord matches whole words of the path, split on separators and camelCase, so `author.ts` is not auth.
func hasAuthWord(p string) bool {
	var word []rune
	flush := func() bool {
		w := strings.ToLower(string(word))
		word = word[:0]
		return authWords[w]
	}
	prev := rune(0)
	for _, r := range p {
		switch {
		case !unicode.IsLetter(r):
			if flush() {
				return true
			}
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			if flush() {
				return true
			}
			word = append(word, r)
		default:
			word = append(word, r)
		}
		prev = r
	}
	return flush()
}

func dirSegments(p string) map[string]bool {
	segs := strings.Split(strings.ToLower(p), "/")
	return set(segs[:len(segs)-1]...)
}

func anyOf(have, want map[string]bool) bool {
	for w := range want {
		if have[w] {
			return true
		}
	}
	return false
}

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
