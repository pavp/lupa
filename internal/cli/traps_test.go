package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files relative to dir, creating directories as needed.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type trapMap struct {
	Files []struct {
		Path      string   `json:"path"`
		Class     string   `json:"class"`
		Risk      string   `json:"risk"`
		Reason    string   `json:"reason"`
		Importers []string `json:"importers"`
	} `json:"files"`
	Size struct {
		Changed        int `json:"changed"`
		GeneratedFiles int `json:"generated_files"`
	} `json:"size"`
	ReviewNow struct {
		Decision string `json:"decision"`
	} `json:"review_now"`
	ReadingOrder []struct {
		Path string `json:"path"`
	} `json:"reading_order"`
}

func mapTrap(t *testing.T, dir string) (trapMap, string) {
	t.Helper()
	code, stdout, stderr := lupa(dir, "map", "main..feature", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var m trapMap
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatal(err)
	}
	code, md, stderr := lupa(dir, "map", "main..feature")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	return m, md
}

func (m trapMap) file(t *testing.T, path string) (class, risk, reason, importers string) {
	t.Helper()
	for _, f := range m.Files {
		if f.Path == path {
			return f.Class, f.Risk, f.Reason, strings.Join(f.Importers, ",")
		}
	}
	t.Fatalf("%s is not in the map", path)
	return
}

func (m trapMap) position(path string) int {
	for i, e := range m.ReadingOrder {
		if e.Path == path {
			return i
		}
	}
	return -1
}

// TestBackendTraps maps "Add retries to the payment webhook handler". The map must surface the migration,
// the handler and config imported outside the change, the manifest, the lockfile and the test. What it
// cannot judge without a model, such as swallowed errors or an "idempotent" claim, is the review's job.
func TestBackendTraps(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	writeTree(t, dir, map[string]string{
		"tsconfig.json":               `{ "compilerOptions": { "baseUrl": ".", "paths": { "@/*": ["src/*"] } } }`,
		"package.json":                `{ "name": "payments", "dependencies": {} }`,
		"package-lock.json":           "{\n\"lockfileVersion\": 3\n}\n",
		"src/config/timeouts.ts":      "export const webhookTimeout = 5;\n",
		"src/webhook/handler.ts":      "import { webhookTimeout } from '@/config/timeouts';\nexport function handle(): number {\n  return webhookTimeout;\n}\n",
		"src/server.ts":               "import { handle } from './webhook/handler';\nimport { webhookTimeout } from './config/timeouts';\nexport const up = handle() + webhookTimeout;\n",
		"src/jobs/replay.ts":          "import { handle } from '@/webhook/handler';\nexport const replay = () => handle();\n",
		"db/migrations/0041_init.sql": "CREATE TABLE deliveries (id TEXT PRIMARY KEY);\n",
	})
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	writeTree(t, dir, map[string]string{
		"db/migrations/0042_add_attempts.sql": "ALTER TABLE deliveries ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;\n",
		"src/config/timeouts.ts":              "export const webhookTimeout = 30;\n",
		"src/webhook/handler.ts": "import { webhookTimeout } from '@/config/timeouts';\n" +
			"export function handle(): number {\n" +
			"  for (let attempt = 0; attempt < 3; attempt++) {\n" +
			"    try {\n      return webhookTimeout;\n    } catch {\n      // swallowed: retry exhaustion still answers 200\n    }\n  }\n" +
			"  return 200;\n}\n",
		"src/webhook/handler.test.ts": "import { handle } from './handler';\nif (handle() !== 30) throw new Error('timeout');\n",
		"package.json":                `{ "name": "payments", "dependencies": { "p-retry": "^6.0.0" } }`,
		"package-lock.json":           "{\n\"lockfileVersion\": 3,\n\"packages\": {}\n}\n" + strings.Repeat("\n", 40),
	})
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "retries")

	m, md := mapTrap(t, dir)

	if class, risk, _, _ := m.file(t, "db/migrations/0042_add_attempts.sql"); class != "read-first" || risk != "one-way-door" {
		t.Errorf("migration = %s/%s, want read-first one-way-door", class, risk)
	}
	if class, _, reason, importers := m.file(t, "src/webhook/handler.ts"); class != "read-first" ||
		reason != "imported by 2 files outside this change" || importers != "src/jobs/replay.ts,src/server.ts" {
		t.Errorf("handler = %s %q %s, want read-first imported by src/jobs/replay.ts and src/server.ts", class, reason, importers)
	}
	if class, _, _, importers := m.file(t, "src/config/timeouts.ts"); class != "read-first" || importers != "src/jobs/replay.ts,src/server.ts" {
		t.Errorf("unmentioned timeout change = %s %s, want read-first, reached directly by src/server.ts and through the handler by src/jobs/replay.ts", class, importers)
	}
	if class, _, reason, _ := m.file(t, "package.json"); class != "read-first" || reason != "dependency manifest" {
		t.Errorf("manifest = %s %q", class, reason)
	}
	if class, _, _, _ := m.file(t, "package-lock.json"); class != "skip" || m.Size.GeneratedFiles != 1 {
		t.Errorf("lockfile = %s with %d generated files, want skip and excluded from size", class, m.Size.GeneratedFiles)
	}
	if m.position("src/webhook/handler.test.ts") != m.position("src/webhook/handler.ts")-1 {
		t.Errorf("the test must come right before the handler: %+v", m.ReadingOrder)
	}
	if m.ReviewNow.Decision != "review" {
		t.Errorf("review_now = %s, want review", m.ReviewNow.Decision)
	}
	for _, want := range []string{
		"- Rollback: one-way door in `db/migrations/0042_add_attempts.sql`",
		"`db/migrations/0042_add_attempts.sql:1` is hard to roll back: what is the rollback plan?",
		"`src/webhook/handler.ts:3` is imported by 2 files outside this change: were they checked?",
		"- Verification: 1 test file changed (`src/webhook/handler.test.ts`)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	for _, notTheMapsJob := range []string{"swallow", "idempot", "200"} {
		if strings.Contains(strings.ToLower(md), notTheMapsJob) {
			t.Errorf("the map must not judge code (%q); that is the agent review's job", notTheMapsJob)
		}
	}
}

// TestFrontendTraps maps a change to a shared Badge that now throws without a size. The map must surface
// the four untouched callers, a test named after Badge that never imports it, the orphan snapshot and
// the lockfile. Whether "no visual changes" holds is the agent review's call, not the map's.
func TestFrontendTraps(t *testing.T) {
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	badge := "export function Badge(label: string, size = 'md'): string {\n  return `[${size}] ${label}`;\n}\n"
	writeTree(t, dir, map[string]string{
		"tsconfig.json":                                  `{}`,
		"package-lock.json":                              "{}\n",
		"src/components/Badge.tsx":                       badge,
		"src/components/Icon.tsx":                        "export const Icon = () => 'icon';\n",
		"src/components/Header.tsx":                      "import { Badge } from './Badge';\nexport const Header = () => Badge('new');\n",
		"src/pages/Home.tsx":                             "import { Badge } from '../components/Badge';\nexport const Home = () => Badge('home');\n",
		"src/pages/Cart.tsx":                             "import { Badge } from '../components/Badge';\nexport const Cart = () => Badge('cart');\n",
		"src/pages/Profile.tsx":                          "import { Badge } from '../components/Badge';\nexport const Profile = () => Badge('me');\n",
		"src/components/__snapshots__/Old.test.tsx.snap": "exports[`Old 1`] = `old`;\n",
	})
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	writeTree(t, dir, map[string]string{
		"src/components/Badge.tsx": "export function Badge(label: string, size?: 'sm' | 'md'): string {\n" +
			"  if (!size) throw new Error('size is required');\n  return `[${size}] ${label}`;\n}\n",
		"src/components/Badge.test.tsx":                  "import { Icon } from './Icon';\nif (Icon() !== 'icon') throw new Error('icon');\n",
		"src/components/__snapshots__/Old.test.tsx.snap": "exports[`Old 1`] = `older`;\n",
		"package-lock.json":                              "{\n\"lockfileVersion\": 3\n}\n",
	})
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "badge requires size; no visual changes")

	m, md := mapTrap(t, dir)

	if class, _, reason, importers := m.file(t, "src/components/Badge.tsx"); class != "read-first" ||
		reason != "imported by 4 files outside this change" ||
		importers != "src/components/Header.tsx,src/pages/Cart.tsx,src/pages/Home.tsx,src/pages/Profile.tsx" {
		t.Errorf("Badge = %s %q %s, want read-first with its four untouched callers", class, reason, importers)
	}
	if class, _, _, _ := m.file(t, "src/components/__snapshots__/Old.test.tsx.snap"); class != "skip" {
		t.Errorf("orphan snapshot = %s, want skip", class)
	}
	if class, _, _, _ := m.file(t, "package-lock.json"); class != "skip" || m.Size.GeneratedFiles != 2 {
		t.Errorf("lockfile = %s with %d generated files, want skip and 2 generated (lockfile, snapshot)", class, m.Size.GeneratedFiles)
	}
	for _, want := range []string{
		"- Verification: `src/components/Badge.test.tsx` does not import `src/components/Badge.tsx`",
		"`src/components/Badge.test.tsx:1` is named like a test of `src/components/Badge.tsx` but does not import it: does it exercise it?",
		"`src/components/Badge.tsx:1` is imported by 4 files outside this change: were they checked?",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "test of src/components/Badge.tsx: read it first") {
		t.Errorf("a test that never imports Badge must not be presented as Badge's test:\n%s", md)
	}
}
