package verify

import (
	"maps"
	"slices"
	"strings"
	"unicode"
)

// Causality values: how a comment's anchored lines relate to the change.
const (
	CausalityIntroduced        string = "introduced"
	CausalityBehaviorActivated string = "behavior-activated"
	CausalityPreExisting       string = "pre-existing"
	CausalityMoved             string = "moved"
	CausalityMovedWithEdit     string = "moved-with-edit"
	CausalityUnknown           string = "unknown"
)

// Level values, derived by code and never taken from the model.
const (
	LevelBlocking  string = "blocking"
	LevelImportant string = "important"
	LevelNote      string = "note"
	LevelFollowUp  string = "follow-up"
)

// Causality classifies a located comment against the unified patch of its file:
// removed code or an anchored added line is introduced, added lines whose every significant line
// was removed elsewhere in the change are moved (a pure move) or moved-with-edit (their hunk also
// changes other code, so the change owns the new place), unchanged lines inside a
// hunk (next to the change) are behavior-activated, unchanged lines outside every
// hunk are pre-existing, and a file without hunks is unknown.
func Causality(loc Location, patches map[string]string) string {
	scan := scanPatch(patches[loc.File])
	if !scan.hasHunks {
		return CausalityUnknown
	}
	switch loc.Side {
	case "old":
		return CausalityIntroduced
	case "new":
		if c, ok := addedCausality(loc, scan, patches); ok {
			return c
		}
		for n := loc.Start; n <= loc.End; n++ {
			if scan.inHunk[n] {
				return CausalityBehaviorActivated
			}
		}
	}
	return CausalityPreExisting
}

// addedCausality classifies a location that holds added lines. Its significant added lines all removed
// elsewhere in the change make it moved: a pure move when its hunk changes nothing else, a move with an
// edit otherwise. Any other significant added line makes it introduced. ok is false without added lines.
func addedCausality(loc Location, scan patchScan, patches map[string]string) (c string, ok bool) {
	var moves moveIndex
	significant := 0
	for n := loc.Start; n <= loc.End; n++ {
		text, isAdded := scan.added[n]
		if !isAdded {
			continue
		}
		ok = true
		key, sig := moveKey(text)
		if !sig {
			continue
		}
		if moves.removed == nil {
			moves = indexMoves(patches)
		}
		if !moves.removed[key] {
			return CausalityIntroduced, true
		}
		significant++
	}
	switch {
	case !ok || significant == 0:
		return CausalityIntroduced, ok
	case scan.hunks[scan.hunkOf[loc.Start]].pureMove(moves):
		return CausalityMoved, true
	default:
		return CausalityMovedWithEdit, true
	}
}

// moveIndex holds the move keys of the significant lines added and removed anywhere in the change.
type moveIndex struct{ added, removed map[string]bool }

func indexMoves(patches map[string]string) moveIndex {
	m := moveIndex{added: map[string]bool{}, removed: map[string]bool{}}
	for _, p := range patches {
		for _, h := range scanPatch(p).hunks {
			for _, l := range h.added {
				if key, sig := moveKey(l); sig {
					m.added[key] = true
				}
			}
			for _, l := range h.removed {
				if key, sig := moveKey(l); sig {
					m.removed[key] = true
				}
			}
		}
	}
	return m
}

// hunkLines are the added and removed lines of one hunk.
type hunkLines struct{ added, removed []string }

// pureMove reports a hunk whose every significant added line was removed elsewhere and whose every
// significant removed line was added elsewhere: code changed place and nothing around it changed.
func (h hunkLines) pureMove(m moveIndex) bool {
	for _, l := range h.added {
		if key, sig := moveKey(l); sig && !m.removed[key] {
			return false
		}
	}
	for _, l := range h.removed {
		if key, sig := moveKey(l); sig && !m.added[key] {
			return false
		}
	}
	return true
}

// moveKeyMin is the alphanumeric characters a line needs to count as moved; shorter lines such as a
// lone brace or "sum := 0" repeat everywhere and would mark unrelated code as moved.
const moveKeyMin = 10

// moveKey returns a line with its whitespace collapsed, so reindented code still matches, and whether
// it is significant enough to identify a move.
func moveKey(line string) (key string, significant bool) {
	alnum := 0
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			alnum++
		}
	}
	return strings.Join(strings.Fields(line), " "), alnum >= moveKeyMin
}

// AddedLines returns the sorted head line numbers of a unified diff's added lines.
func AddedLines(patch string) []int {
	return slices.Sorted(maps.Keys(scanPatch(patch).added))
}

// patchScan is a unified diff read line by line: its added lines by head line number, every head line
// its hunks show, the hunk each head line belongs to, each hunk's added and removed lines, and whether
// the diff has any hunk at all.
type patchScan struct {
	added    map[int]string
	inHunk   map[int]bool
	hunkOf   map[int]int
	hunks    []hunkLines
	hasHunks bool
}

func scanPatch(patch string) patchScan {
	s := patchScan{added: map[int]string{}, inHunk: map[int]bool{}, hunkOf: map[int]int{}}
	var newN, oldLeft, newLeft int
	for _, l := range strings.Split(patch, "\n") {
		if oldLeft == 0 && newLeft == 0 {
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			s.hasHunks = true
			s.hunks = append(s.hunks, hunkLines{})
			oldLeft, newN, newLeft = count(m[2]), atoi(m[3]), count(m[4])
			continue
		}
		h := &s.hunks[len(s.hunks)-1]
		marker := byte(' ')
		if l != "" {
			marker = l[0]
		}
		switch marker {
		case ' ':
			s.inHunk[newN], s.hunkOf[newN] = true, len(s.hunks)-1
			newN, newLeft, oldLeft = newN+1, newLeft-1, oldLeft-1
		case '+':
			s.added[newN], s.inHunk[newN], s.hunkOf[newN] = l[1:], true, len(s.hunks)-1
			h.added = append(h.added, l[1:])
			newN, newLeft = newN+1, newLeft-1
		case '-':
			h.removed = append(h.removed, l[1:])
			oldLeft--
		}
	}
	return s
}

// Observable reports a harm a user or caller would see: wrong data, a failure, a security hole or a broken caller.
func Observable(harm string) bool { return slices.Contains(Harms[:4], harm) }

// Level derives a comment's level from its severity, evidence class and causality only, so the model
// cannot promote its own comment by its wording. The harm label does not decide it: the reviewer gives
// almost every candidate an observable harm, which in measurement tripled the main list and its wrong items.
func Level(c Comment, causality string) string {
	switch {
	case causality == CausalityPreExisting || causality == CausalityMoved:
		return LevelFollowUp
	case c.Severity != "severe":
		return LevelNote
	case causality == CausalityUnknown || !c.QuoteSettles:
		return LevelImportant
	default:
		return LevelBlocking
	}
}
