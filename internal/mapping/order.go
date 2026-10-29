package mapping

import (
	"path"
	"sort"
	"strings"
)

// ReadingEntry is one file in the reading order, with why it sits there.
type ReadingEntry struct {
	Path    string `json:"path"`
	Section Class  `json:"section"`
	Why     string `json:"why"`
}

// readingOrder puts read-first files first (one-way doors before impact), then skim, then skip.
// A test goes right before the file it tests, in that file's section; a test whose subject is
// not in the change opens the skim section.
func readingOrder(files []File) []ReadingEntry {
	var code, tests []File
	for _, f := range files {
		if isTestPath(f.Path) && f.Class != Skip {
			tests = append(tests, f)
		} else {
			code = append(code, f)
		}
	}
	rank := map[Class]int{ReadFirst: 0, Skim: 1, Skip: 2}
	sort.SliceStable(code, func(i, j int) bool {
		a, b := code[i], code[j]
		if rank[a.Class] != rank[b.Class] {
			return rank[a.Class] < rank[b.Class]
		}
		if (a.Risk == OneWayDoor) != (b.Risk == OneWayDoor) {
			return a.Risk == OneWayDoor
		}
		return a.Path < b.Path
	})
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].Path < tests[j].Path })

	before := map[string][]File{}
	var orphans []File
	for _, t := range tests {
		if subject, ok := subjectOf(t.Path, code); ok {
			before[subject] = append(before[subject], t)
		} else {
			orphans = append(orphans, t)
		}
	}
	sort.SliceStable(orphans, func(i, j int) bool { return orphans[i].Class == ReadFirst && orphans[j].Class != ReadFirst })

	var out []ReadingEntry
	orphansPlaced := false
	placeOrphans := func() {
		for _, t := range orphans {
			section := Skim
			if t.Class == ReadFirst {
				section = ReadFirst
			}
			out = append(out, ReadingEntry{t.Path, section, "test whose subject is not in this change"})
		}
		orphansPlaced = true
	}
	for _, f := range code {
		if !orphansPlaced && f.Class != ReadFirst {
			placeOrphans()
		}
		for _, t := range before[f.Path] {
			out = append(out, ReadingEntry{t.Path, f.Class, "test of " + f.Path + ": read it first to know what the code must do"})
		}
		out = append(out, ReadingEntry{f.Path, f.Class, f.Reason})
	}
	if !orphansPlaced {
		placeOrphans()
	}
	return out
}

// subjectOf finds the changed file a test exercises by matching stems: the same directory first,
// then the parent of a test directory, then a single match anywhere.
func subjectOf(test string, code []File) (string, bool) {
	stem := testStem(path.Base(test))
	dir := path.Dir(test)
	parent := path.Dir(dir)
	var anywhere []string
	for _, f := range code {
		if f.Class == Skip || codeStem(path.Base(f.Path)) != stem {
			continue
		}
		switch path.Dir(f.Path) {
		case dir, parent:
			return f.Path, true
		}
		anywhere = append(anywhere, f.Path)
	}
	if len(anywhere) == 1 {
		return anywhere[0], true
	}
	return "", false
}

func testStem(base string) string {
	base = strings.TrimPrefix(base, "test_")
	for _, marker := range []string{".test.", ".spec.", "_test."} {
		if i := strings.Index(base, marker); i >= 0 {
			return base[:i]
		}
	}
	return codeStem(base)
}

func codeStem(base string) string {
	if i := strings.Index(base, "."); i > 0 {
		return base[:i]
	}
	return base
}

// isTestPath recognizes a test by its path even when an earlier rule, such as auth, classified it.
func isTestPath(p string) bool {
	return isTest(strings.ToLower(path.Base(p)), dirSegments(p))
}
