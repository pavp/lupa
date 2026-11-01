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
// was removed elsewhere in the change are moved, unchanged lines inside a
// hunk (next to the change) are behavior-activated, unchanged lines outside every
// hunk are pre-existing, and a file without hunks is unknown.
func Causality(loc Location, patches map[string]string) string {
	added, _, inHunk, hasHunks := scanPatch(patches[loc.File])
	if !hasHunks {
		return CausalityUnknown
	}
	switch loc.Side {
	case "old":
		return CausalityIntroduced
	case "new":
		if c, ok := addedCausality(loc, added, patches); ok {
			return c
		}
		for n := loc.Start; n <= loc.End; n++ {
			if inHunk[n] {
				return CausalityBehaviorActivated
			}
		}
	}
	return CausalityPreExisting
}

// addedCausality classifies a location that holds added lines: moved when each of its significant added lines
// matches a line removed anywhere in the change, else introduced. ok is false when it holds no added line.
func addedCausality(loc Location, added map[int]string, patches map[string]string) (c string, ok bool) {
	var removed map[string]bool
	significant := 0
	for n := loc.Start; n <= loc.End; n++ {
		text, isAdded := added[n]
		if !isAdded {
			continue
		}
		ok = true
		key, sig := moveKey(text)
		if !sig {
			continue
		}
		if removed == nil {
			removed = removedKeys(patches)
		}
		if !removed[key] {
			return CausalityIntroduced, true
		}
		significant++
	}
	if ok && significant > 0 {
		return CausalityMoved, true
	}
	return CausalityIntroduced, ok
}

// removedKeys returns the move keys of the significant lines removed anywhere in the change.
func removedKeys(patches map[string]string) map[string]bool {
	keys := map[string]bool{}
	for _, p := range patches {
		_, removed, _, _ := scanPatch(p)
		for _, l := range removed {
			if key, sig := moveKey(l); sig {
				keys[key] = true
			}
		}
	}
	return keys
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
	added, _, _, _ := scanPatch(patch)
	return slices.Sorted(maps.Keys(added))
}

// scanPatch returns a unified diff's added lines by head line number, its removed lines, every
// head line its hunks show, and whether the diff has any hunk at all.
func scanPatch(patch string) (added map[int]string, removed []string, inHunk map[int]bool, hasHunks bool) {
	added, inHunk = map[int]string{}, map[int]bool{}
	var newN, oldLeft, newLeft int
	for _, l := range strings.Split(patch, "\n") {
		if oldLeft == 0 && newLeft == 0 {
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			hasHunks = true
			oldLeft, newN, newLeft = count(m[2]), atoi(m[3]), count(m[4])
			continue
		}
		marker := byte(' ')
		if l != "" {
			marker = l[0]
		}
		switch marker {
		case ' ':
			inHunk[newN] = true
			newN, newLeft, oldLeft = newN+1, newLeft-1, oldLeft-1
		case '+':
			added[newN], inHunk[newN] = l[1:], true
			newN, newLeft = newN+1, newLeft-1
		case '-':
			removed = append(removed, l[1:])
			oldLeft--
		}
	}
	return added, removed, inHunk, hasHunks
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
