package cli

import (
	"encoding/json"
	"os"
	"testing"
)

// TestReviewWithRealClaude runs the whole review against the real Claude Code CLI on the user's
// subscription. It costs a few cents, so it runs only with LUPA_E2E=1.
func TestReviewWithRealClaude(t *testing.T) {
	if os.Getenv("LUPA_E2E") != "1" {
		t.Skip("set LUPA_E2E=1 to run the review against the real claude CLI")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	loop := func(header string) string {
		return "export interface Item { price: number; qty: number }\n\n" +
			"export function total(items: Item[]): number {\n  let sum = 0;\n" + header +
			"    sum += items[i].price * items[i].qty;\n  }\n  return sum;\n}\n"
	}
	writeTree(t, dir, map[string]string{
		"tsconfig.json":   "{}\n",
		"src/cart.ts":     loop("  for (let i = 0; i < items.length; i++) {\n"),
		"src/checkout.ts": "import { total } from './cart';\nexport const checkout = (items) => total(items);\n",
	})
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-q", "-m", "base")
	gitCmd(t, dir, "switch", "-q", "-c", "feature")
	writeTree(t, dir, map[string]string{"src/cart.ts": loop("  for (let i = 1; i < items.length; i++) {\n")})
	gitCmd(t, dir, "commit", "-q", "-am", "skip the first item")

	code, stdout, stderr := lupa(dir, "review", "feature", "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	var got struct {
		Findings []struct {
			File  string `json:"file"`
			Start int    `json:"start"`
			End   int    `json:"end"`
			Level string `json:"level"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not the review JSON: %v\n%s", err, stdout)
	}
	for _, f := range got.Findings {
		if f.File == "src/cart.ts" && f.Start <= 5 && f.End >= 5 && (f.Level == "blocking" || f.Level == "important") {
			return
		}
	}
	t.Errorf("the off-by-one at src/cart.ts:5 was not reported as blocking or important: %+v", got.Findings)
}
