package review

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/lupa/internal/mapping"
)

func header(path string) string { return fmt.Sprintf("diff --git a/%s b/%s\n", path, path) }

// hunk returns a hunk starting at line start on both sides, exactly size bytes long.
func hunk(start, size int) string {
	h := fmt.Sprintf("@@ -%d +%d @@\n", start, start)
	return h + "+" + strings.Repeat("x", size-len(h)-2) + "\n"
}

func file(path string) mapping.File { return mapping.File{Path: path, Class: mapping.Skim} }

// layout renders the groups as "id:path@hunks|path" for compact comparison.
func layout(r Result) []string {
	var out []string
	for _, g := range r.Groups {
		var parts []string
		for _, p := range g.Parts {
			s := p.Path
			if len(p.Hunks) > 0 {
				s += "@" + strings.Trim(strings.Join(strings.Fields(fmt.Sprint(p.Hunks)), ","), "[]")
			}
			parts = append(parts, s)
		}
		out = append(out, fmt.Sprintf("%d:%s", g.ID, strings.Join(parts, "|")))
	}
	return out
}

func TestPlan(t *testing.T) {
	big := "a/big.go"
	bigHeader := header(big)
	cases := []struct {
		name    string
		files   []mapping.File
		patches map[string]string
		budget  int
		groups  []string
		ledger  []Entry
	}{
		{
			name:    "files of one directory share a group",
			files:   []mapping.File{file("a/y.go"), file("a/x.go")},
			patches: map[string]string{"a/x.go": header("a/x.go") + hunk(1, 20), "a/y.go": header("a/y.go") + hunk(1, 20)},
			budget:  1000,
			groups:  []string{"1:a/x.go|a/y.go"},
			ledger:  []Entry{{Path: "a/x.go", Status: Reviewed, Groups: []int{1}}, {Path: "a/y.go", Status: Reviewed, Groups: []int{1}}},
		},
		{
			name:    "budget overflow starts a new group",
			files:   []mapping.File{file("a/x.go"), file("a/y.go")},
			patches: map[string]string{"a/x.go": hunk(1, 60), "a/y.go": hunk(1, 60)},
			budget:  100,
			groups:  []string{"1:a/x.go", "2:a/y.go"},
			ledger:  []Entry{{Path: "a/x.go", Status: Reviewed, Groups: []int{1}}, {Path: "a/y.go", Status: Reviewed, Groups: []int{2}}},
		},
		{
			name:    "different directories get separate groups",
			files:   []mapping.File{file("b/y.go"), file("a/x.go")},
			patches: map[string]string{"a/x.go": hunk(1, 20), "b/y.go": hunk(1, 20)},
			budget:  1000,
			groups:  []string{"1:a/x.go", "2:b/y.go"},
			ledger:  []Entry{{Path: "a/x.go", Status: Reviewed, Groups: []int{1}}, {Path: "b/y.go", Status: Reviewed, Groups: []int{2}}},
		},
		{
			name:    "a file over the budget is split by hunks",
			files:   []mapping.File{file(big)},
			patches: map[string]string{big: bigHeader + hunk(1, 30) + hunk(10, 30) + hunk(20, 30)},
			budget:  len(bigHeader) + 60,
			groups:  []string{"1:a/big.go@1,10", "2:a/big.go@20"},
			ledger:  []Entry{{Path: big, Status: Reviewed, Groups: []int{1, 2}}},
		},
		{
			name:    "a single hunk over the budget is left out, the rest reviewed",
			files:   []mapping.File{file(big)},
			patches: map[string]string{big: bigHeader + hunk(1, 30) + hunk(10, 200) + hunk(20, 30)},
			budget:  100,
			groups:  []string{"1:a/big.go@1,20"},
			ledger: []Entry{
				{Path: big, Status: Reviewed, Groups: []int{1}},
				{Path: big, Status: NotReviewed, Reason: fmt.Sprintf("hunk at line 10 exceeds the prompt budget (%d bytes > 100)", len(bigHeader)+200)},
			},
		},
		{
			name: "generated and binary files are skipped with a reason",
			files: []mapping.File{
				{Path: "package-lock.json", Class: mapping.Skip, Reason: "lockfile"},
				{Path: "logo.png", Class: mapping.Skim, Binary: true},
			},
			patches: map[string]string{"package-lock.json": hunk(1, 20)},
			budget:  1000,
			ledger: []Entry{
				{Path: "logo.png", Status: Skipped, Reason: "binary file"},
				{Path: "package-lock.json", Status: Skipped, Reason: "generated: lockfile"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Plan(c.files, c.patches, Options{Budget: c.budget})
			if got := layout(r); !slices.Equal(got, c.groups) {
				t.Errorf("groups = %v, want %v", got, c.groups)
			}
			if !reflect.DeepEqual(r.Ledger, c.ledger) {
				t.Errorf("ledger = %+v, want %+v", r.Ledger, c.ledger)
			}
			for _, g := range r.Groups {
				if g.Bytes > c.budget {
					t.Errorf("group %d has %d bytes, over the budget %d", g.ID, g.Bytes, c.budget)
				}
			}
		})
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	files := []mapping.File{file("b/z.go"), file("a/y.go"), file("a/x.go"), file("c.go")}
	patches := map[string]string{"b/z.go": hunk(1, 40), "a/y.go": hunk(1, 40), "a/x.go": hunk(1, 40), "c.go": hunk(1, 40)}
	want := Plan(files, patches, Options{Budget: 100})
	reversed := slices.Clone(files)
	slices.Reverse(reversed)
	if got := Plan(reversed, patches, Options{Budget: 100}); !reflect.DeepEqual(got, want) {
		t.Errorf("shuffled input changed the plan:\n%v\nwant\n%v", layout(got), layout(want))
	}
	if got := layout(want); !slices.Equal(got, []string{"1:c.go", "2:a/x.go|a/y.go", "3:b/z.go"}) {
		t.Errorf("groups = %v", got)
	}
}

func TestPlanEstimate(t *testing.T) {
	r := Plan([]mapping.File{file("a.go"), file("b/c.go")}, map[string]string{"a.go": hunk(1, 101), "b/c.go": hunk(1, 50)}, Options{})
	if r.Budget != DefaultBudget {
		t.Errorf("budget = %d, want %d", r.Budget, DefaultBudget)
	}
	want := Estimate{Runs: 3, Tokens: (101+4000+3)/4 + (50+4000+3)/4 + 2*ReadingTokens}
	if r.Estimate != want {
		t.Errorf("estimate = %+v, want %+v", r.Estimate, want)
	}
	if empty := Plan(nil, nil, Options{}); empty.Estimate != (Estimate{}) {
		t.Errorf("empty estimate = %+v, want zero", empty.Estimate)
	}
	measured := Plan([]mapping.File{file("a.go")}, map[string]string{"a.go": hunk(1, 101)}, Options{Overhead: 9000})
	if want := (Estimate{Runs: 2, Tokens: (101+9000+3)/4 + ReadingTokens}); measured.Estimate != want {
		t.Errorf("estimate with a measured overhead = %+v, want %+v", measured.Estimate, want)
	}
}
