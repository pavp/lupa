package verify

import (
	"sort"
	"strings"
	"unicode"
)

// Finding is a verified comment with what later stages carry along.
type Finding struct {
	Comment   Comment
	Location  Location
	Causality string
	Level     string
	Merged    int
}

const claimSimilarity = 0.5

var levelRank = map[string]int{
	LevelBlocking:  4,
	LevelImportant: 3,
	LevelNote:      2,
	LevelFollowUp:  1,
}

type dedupeEntry struct {
	index   int
	finding Finding
	words   map[string]bool
}

// Dedupe merges findings on the same file and side whose ranges overlap and
// whose claims are similar. The survivor is the higher level (ties: the earlier
// one), keeps its own text, widens its range and counts what it absorbed.
// Merging repeats until stable, because a widened range can reach a further one.
func Dedupe(fs []Finding) []Finding {
	if len(fs) == 0 {
		return nil
	}
	entries := make([]dedupeEntry, len(fs))
	for i, f := range fs {
		entries[i] = dedupeEntry{index: i, finding: f, words: claimWords(f.Comment.Claim)}
	}
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(entries) && !merged; i++ {
			for j := i + 1; j < len(entries); j++ {
				if duplicates(entries[i], entries[j]) {
					entries[i] = mergeEntries(entries[i], entries[j])
					entries = append(entries[:j], entries[j+1:]...)
					merged = true
					break
				}
			}
		}
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].index < entries[b].index })
	out := make([]Finding, len(entries))
	for i, e := range entries {
		out[i] = e.finding
	}
	return out
}

func duplicates(a, b dedupeEntry) bool {
	la, lb := a.finding.Location, b.finding.Location
	if la.File != lb.File || la.Side != lb.Side {
		return false
	}
	if la.Start > lb.End || lb.Start > la.End {
		return false
	}
	return jaccard(a.words, b.words) >= claimSimilarity
}

func mergeEntries(a, b dedupeEntry) dedupeEntry {
	winner, loser := a, b
	if levelRank[b.finding.Level] > levelRank[a.finding.Level] ||
		(levelRank[b.finding.Level] == levelRank[a.finding.Level] && b.index < a.index) {
		winner, loser = b, a
	}
	loc := &winner.finding.Location
	loc.Start = min(loc.Start, loser.finding.Location.Start)
	loc.End = max(loc.End, loser.finding.Location.End)
	winner.finding.Merged += loser.finding.Merged + 1
	return winner
}

func claimWords(claim string) map[string]bool {
	words := map[string]bool{}
	fields := strings.FieldsFunc(strings.ToLower(claim), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range fields {
		if len([]rune(w)) >= 3 {
			words[w] = true
		}
	}
	return words
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}
