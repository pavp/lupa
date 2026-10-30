package verify

import (
	"maps"
	"slices"
	"strings"
)

// Causality values: how a comment's anchored lines relate to the change.
const (
	CausalityIntroduced        string = "introduced"
	CausalityBehaviorActivated string = "behavior-activated"
	CausalityPreExisting       string = "pre-existing"
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
// removed code or an anchored added line is introduced, unchanged lines inside a
// hunk (next to the change) are behavior-activated, unchanged lines outside every
// hunk are pre-existing, and a file without hunks is unknown.
func Causality(loc Location, patches map[string]string) string {
	added, inHunk, hasHunks := addedLines(patches[loc.File])
	if !hasHunks {
		return CausalityUnknown
	}
	switch loc.Side {
	case "old":
		return CausalityIntroduced
	case "new":
		for n := loc.Start; n <= loc.End; n++ {
			if added[n] {
				return CausalityIntroduced
			}
		}
		for n := loc.Start; n <= loc.End; n++ {
			if inHunk[n] {
				return CausalityBehaviorActivated
			}
		}
	}
	return CausalityPreExisting
}

// AddedLines returns the sorted head line numbers of a unified diff's added lines.
func AddedLines(patch string) []int {
	added, _, _ := addedLines(patch)
	return slices.Sorted(maps.Keys(added))
}

// addedLines returns the head line numbers of a unified diff's added lines, of every
// head line its hunks show, and whether the diff has any hunk at all.
func addedLines(patch string) (added, inHunk map[int]bool, hasHunks bool) {
	added, inHunk = map[int]bool{}, map[int]bool{}
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
			added[newN], inHunk[newN] = true, true
			newN, newLeft = newN+1, newLeft-1
		case '-':
			oldLeft--
		}
	}
	return added, inHunk, hasHunks
}

// Level derives a comment's level from its severity, evidence class and
// causality only, so the model cannot promote its own comment.
func Level(c Comment, causality string) string {
	switch {
	case causality == CausalityPreExisting:
		return LevelFollowUp
	case c.Severity != "severe":
		return LevelNote
	case causality == CausalityUnknown || !c.QuoteSettles:
		return LevelImportant
	default:
		return LevelBlocking
	}
}
