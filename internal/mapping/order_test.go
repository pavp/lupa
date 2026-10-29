package mapping

import (
	"fmt"
	"strings"
	"testing"
)

func TestReadingOrder(t *testing.T) {
	s := Build(Input{Base: "b", Head: "h", Files: []File{
		{Path: "README.md"},
		{Path: "package-lock.json"},
		{Path: "src/auth/login.test.ts"},
		{Path: "src/auth/login.ts"},
		{Path: "src/orphan.test.ts"},
		{Path: "src/user.test.ts"},
		{Path: "src/user.ts"},
		{Path: "src/x/__tests__/cart.ts"},
		{Path: "src/x/cart.ts"},
		{Path: "src/auth/policy.ts"},
		{Path: "db/migrations/001.sql"},
		{Path: "src/session/expiry.test.ts"},
	}})
	want := []string{
		"read-first db/migrations/001.sql",
		"read-first src/auth/login.test.ts",
		"read-first src/auth/login.ts",
		"read-first src/auth/policy.ts",
		"read-first src/session/expiry.test.ts",
		"skim src/orphan.test.ts",
		"skim README.md",
		"skim src/user.test.ts",
		"skim src/user.ts",
		"skim src/x/__tests__/cart.ts",
		"skim src/x/cart.ts",
		"skip package-lock.json",
	}
	var got []string
	for _, e := range s.ReadingOrder {
		got = append(got, fmt.Sprintf("%s %s", e.Section, e.Path))
		if e.Why == "" {
			t.Errorf("%s has no why", e.Path)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("reading order:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range s.ReadingOrder {
		switch e.Path {
		case "src/auth/login.test.ts":
			if !strings.Contains(e.Why, "src/auth/login.ts") {
				t.Errorf("why of a test must name its subject, got %q", e.Why)
			}
		case "src/orphan.test.ts":
			if !strings.Contains(e.Why, "not in this change") {
				t.Errorf("why of an orphan test must say its subject is not in the change, got %q", e.Why)
			}
		}
	}
}
