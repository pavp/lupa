// Package review plans and runs the agent review of a change. The plan is pure: data in, data out.
package review

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/pavp/lupa/internal/mapping"
)

// DefaultBudget is the patch bytes one group may carry into a reviewer prompt.
const DefaultBudget = 60000

// defaultOverhead stands in for the fixed part of a reviewer prompt when Options.Overhead is zero.
const defaultOverhead = 4000

// ReadingTokens stands in for what one reviewer reads with its tools beyond its prompt: callers,
// importers, nearby files, counted with cache reads. Measured on a 29-file React Native change:
// 125k to 240k input tokens per reviewer run.
const ReadingTokens = 150000

// Status says what happens to a changed file in the review.
type Status string

const (
	Reviewed    Status = "reviewed"
	Skipped     Status = "skipped"
	NotReviewed Status = "not-reviewed"
)

// Options tune the plan; a zero Budget means DefaultBudget. Overhead is the bytes of a reviewer
// prompt other than its patch, used by the estimate.
type Options struct{ Budget, Overhead int }

// Part is a file, or some of its hunks, inside a group. Hunks holds the head-side start lines of
// the hunks the part carries, and is empty when the part is the whole file.
type Part struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
	Hunks []int  `json:"hunks,omitempty"`
	Patch string `json:"-"`
}

// Group is the patch one reviewer run reads.
type Group struct {
	ID    int    `json:"id"`
	Bytes int    `json:"bytes"`
	Parts []Part `json:"parts"`
}

// Entry is one line of the coverage ledger. A file with a hunk left out has a reviewed entry
// for the rest of it and a not-reviewed entry per hunk left out.
type Entry struct {
	Path   string `json:"path"`
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	Groups []int  `json:"groups,omitempty"`
}

// Estimate is an approximation of the agent runs and tokens the review will spend, not a measurement.
type Estimate struct {
	Runs   int `json:"runs"`
	Tokens int `json:"tokens"`
}

// Result is the plan of one review: the groups to run, the coverage ledger and the estimate.
type Result struct {
	Budget   int      `json:"budget_bytes"`
	Groups   []Group  `json:"groups"`
	Ledger   []Entry  `json:"ledger"`
	Estimate Estimate `json:"estimate"`
}

// Plan selects the reviewable files and packs their patches, keyed by path, into groups under the byte budget.
// A change whose reviewable patches fit the budget is one group: with a reviewer that reports every plausible
// defect and a scorer behind it, one whole-change run was measured as precise as many and cheaper. A larger
// change is cut by directory first, then by the budget. A file over the budget is split by hunks, and only a
// hunk that alone exceeds the budget is left out.
func Plan(files []mapping.File, patches map[string]string, opts Options) Result {
	budget := cmp.Or(opts.Budget, DefaultBudget)
	r := Result{Budget: budget, Groups: []Group{}, Ledger: []Entry{}}
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b mapping.File) int {
		return cmp.Or(cmp.Compare(path.Dir(a.Path), path.Dir(b.Path)), cmp.Compare(a.Path, b.Path))
	})
	total := 0
	for _, f := range sorted {
		if f.Class != mapping.Skip && !f.Binary && !isDoc(f.Path) {
			total += len(patches[f.Path])
		}
	}
	whole := total <= budget
	dir := ""
	for _, f := range sorted {
		switch {
		case f.Class == mapping.Skip:
			r.Ledger = append(r.Ledger, Entry{Path: f.Path, Status: Skipped, Reason: "generated: " + f.Reason})
			continue
		case f.Binary:
			r.Ledger = append(r.Ledger, Entry{Path: f.Path, Status: Skipped, Reason: "binary file"})
			continue
		case isDoc(f.Path):
			r.Ledger = append(r.Ledger, Entry{Path: f.Path, Status: Skipped, Reason: "documentation"})
			continue
		}
		parts, left := split(f.Path, patches[f.Path], budget)
		reviewed := Entry{Path: f.Path, Status: Reviewed}
		for _, p := range parts {
			n := len(r.Groups)
			if n == 0 || (!whole && path.Dir(f.Path) != dir) || r.Groups[n-1].Bytes+p.Bytes > budget {
				r.Groups = append(r.Groups, Group{ID: n + 1, Parts: []Part{}})
				n++
			}
			dir = path.Dir(f.Path)
			g := &r.Groups[n-1]
			g.Parts = append(g.Parts, p)
			g.Bytes += p.Bytes
			if !slices.Contains(reviewed.Groups, g.ID) {
				reviewed.Groups = append(reviewed.Groups, g.ID)
			}
		}
		if len(parts) > 0 {
			r.Ledger = append(r.Ledger, reviewed)
		}
		r.Ledger = append(r.Ledger, left...)
	}
	slices.SortStableFunc(r.Ledger, func(a, b Entry) int { return cmp.Compare(a.Path, b.Path) })
	overhead := cmp.Or(opts.Overhead, defaultOverhead)
	for _, g := range r.Groups {
		r.Estimate.Tokens += (g.Bytes+overhead+3)/4 + ReadingTokens
	}
	if len(r.Groups) > 0 {
		// One reviewer run per group, plus the scorer run over the findings.
		r.Estimate.Runs = len(r.Groups) + 1
	}
	return r
}

// isDoc reports prose documentation, which a code reviewer has nothing to verify in.
func isDoc(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".mdx"
}

// split returns the file as one part when its patch fits the budget, else as parts of consecutive hunks
// under the budget, each repeating the file header. A hunk that alone exceeds the budget is left out.
func split(file, patch string, budget int) (parts []Part, left []Entry) {
	if len(patch) <= budget {
		return []Part{{Path: file, Bytes: len(patch), Patch: patch}}, nil
	}
	head, hunks := hunksOf(patch)
	var cur *Part
	for _, h := range hunks {
		start := startLine(h)
		size := len(head) + len(h)
		if size > budget {
			left = append(left, Entry{Path: file, Status: NotReviewed,
				Reason: fmt.Sprintf("hunk at line %d exceeds the prompt budget (%d bytes > %d)", start, size, budget)})
			continue
		}
		if cur == nil || cur.Bytes+len(h) > budget {
			parts = append(parts, Part{Path: file, Bytes: len(head), Hunks: []int{}, Patch: head})
			cur = &parts[len(parts)-1]
		}
		cur.Patch += h
		cur.Bytes += len(h)
		cur.Hunks = append(cur.Hunks, start)
	}
	return parts, left
}

// hunksOf cuts a file's patch into its header and its hunks at the `@@` lines.
func hunksOf(patch string) (head string, hunks []string) {
	lines := strings.SplitAfter(patch, "\n")
	for _, l := range lines {
		if !strings.HasPrefix(l, "@@") {
			if len(hunks) == 0 {
				head += l
			} else {
				hunks[len(hunks)-1] += l
			}
			continue
		}
		hunks = append(hunks, l)
	}
	return head, hunks
}

// startLine reads the head-side start line of a hunk: `@@ -a,b +c,d @@` gives c.
func startLine(hunk string) int {
	fields := strings.Fields(hunk)
	if len(fields) < 3 {
		return 0
	}
	start, _, _ := strings.Cut(strings.TrimPrefix(fields[2], "+"), ",")
	n, _ := strconv.Atoi(start)
	return n
}
