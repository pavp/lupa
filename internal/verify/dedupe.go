package verify

import (
	"regexp"
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
	symbols map[string]bool
	also    []Location
}

// Dedupe merges findings on the same file and side whose ranges overlap and
// whose claims are similar. The survivor is the higher level (ties: the earlier
// one), keeps its own text, widens its range and counts what it absorbed.
// Merging repeats until stable, because a widened range can reach a further one. A second pass then
// merges findings anywhere in the change that are one defect (see sameDefect); also holds, per survivor,
// the locations of the findings it absorbed that way.
func Dedupe(fs []Finding) (out []Finding, also [][]Location) {
	if len(fs) == 0 {
		return nil, nil
	}
	entries := make([]dedupeEntry, len(fs))
	for i, f := range fs {
		entries[i] = dedupeEntry{index: i, finding: f, words: claimWords(f.Comment.Claim),
			symbols: citedSymbols(f.Comment.Claim + "\n" + f.Comment.Mechanism)}
	}
	entries = mergeWhile(entries, duplicates, mergeEntries)
	// The root cause of one defect is often a symbol that several files use, so the same defect gets
	// reported from each of them.
	entries = mergeWhile(entries, sameDefect, mergeAcross)
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].index < entries[b].index })
	out, also = make([]Finding, len(entries)), make([][]Location, len(entries))
	for i, e := range entries {
		out[i], also[i] = e.finding, e.also
	}
	return out, also
}

// mergeWhile merges the first pair that matches until none does, because a merge can make a further one match.
func mergeWhile(entries []dedupeEntry, match func(a, b dedupeEntry) bool, merge func(a, b dedupeEntry) dedupeEntry) []dedupeEntry {
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(entries) && !merged; i++ {
			for j := i + 1; j < len(entries); j++ {
				if match(entries[i], entries[j]) {
					entries[i] = merge(entries[i], entries[j])
					entries = append(entries[:j], entries[j+1:]...)
					merged = true
					break
				}
			}
		}
	}
	return entries
}

// sameDefect matches findings anywhere in the change that cite a common symbol, share their harm and
// have similar claims; a shared symbol alone is not enough, since one symbol can hold several defects.
func sameDefect(a, b dedupeEntry) bool {
	ca, cb := a.finding.Comment, b.finding.Comment
	if ca.Harm == "" || ca.Harm != cb.Harm || jaccard(a.words, b.words) < claimSimilarity {
		return false
	}
	for s := range a.symbols {
		if b.symbols[s] {
			return true
		}
	}
	return false
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

// mergeAcross keeps the survivor's own location and lists the other finding's locations beside it.
func mergeAcross(a, b dedupeEntry) dedupeEntry {
	winner, loser := survivor(a, b)
	winner.also = append(append(winner.also, loser.finding.Location), loser.also...)
	winner.finding.Merged += loser.finding.Merged + 1
	return winner
}

// survivor returns the higher level of two findings first, the earlier one on a tie.
func survivor(a, b dedupeEntry) (winner, loser dedupeEntry) {
	if levelRank[b.finding.Level] > levelRank[a.finding.Level] ||
		(levelRank[b.finding.Level] == levelRank[a.finding.Level] && b.index < a.index) {
		return b, a
	}
	return a, b
}

func mergeEntries(a, b dedupeEntry) dedupeEntry {
	winner, loser := survivor(a, b)
	winner.also = append(winner.also, loser.also...)
	loc := &winner.finding.Location
	loc.Start = min(loc.Start, loser.finding.Location.Start)
	loc.End = max(loc.End, loser.finding.Location.End)
	winner.finding.Merged += loser.finding.Merged + 1
	return winner
}

var (
	backticked = regexp.MustCompile("`([^`]+)`")
	identifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\()?`)
)

// citedSymbols returns the code identifiers a text names: backticked, camelCase, with an underscore,
// or called. Plain words are not symbols, so "list" or "user" never link two findings.
func citedSymbols(text string) map[string]bool {
	symbols := map[string]bool{}
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		for _, id := range identifier.FindAllString(m[1], -1) {
			symbols[strings.TrimSuffix(id, "(")] = true
		}
	}
	for _, id := range identifier.FindAllString(text, -1) {
		name := strings.TrimSuffix(id, "(")
		if len(name) >= 3 && (name != id || strings.Contains(name, "_") || camel(name)) {
			symbols[name] = true
		}
	}
	return symbols
}

// camel reports a lower-case letter followed by an upper-case one, as in useRemoveUser.
func camel(s string) bool {
	for i := 1; i < len(s); i++ {
		if unicode.IsLower(rune(s[i-1])) && unicode.IsUpper(rune(s[i])) {
			return true
		}
	}
	return false
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
