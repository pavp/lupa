package verify

import (
	"regexp"
	"strconv"
	"strings"
)

// Location is where a comment's quote was found in the reviewed change.
type Location struct {
	File      string
	Side      string
	Start     int
	End       int
	Method    string
	Ambiguous bool
}

type codeLine struct {
	n    int
	text string
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Locate finds the code a comment quotes: the hunks of its file on the new side,
// then the old side, then the file's full head content, then a unique match on
// the new side of another changed file. Matching is exact on whole, consecutive,
// trimmed non-blank lines; a miss returns false.
func Locate(quote, file string, patches map[string]string, head func(path string) (string, bool)) (Location, bool) {
	want := normalizeQuote(quote)
	if len(want) == 0 {
		return Location{}, false
	}
	newSide, oldSide := hunkSides(patches[file])
	if loc, ok := firstMatch(want, newSide); ok {
		return loc.on(file, "new", "new-side"), true
	}
	if loc, ok := firstMatch(want, oldSide); ok {
		return loc.on(file, "old", "old-side"), true
	}
	if content, ok := head(file); ok {
		if loc, ok := firstMatch(want, numberLines(strings.Split(content, "\n"), 1)); ok {
			return loc.on(file, "new", "full-file"), true
		}
	}

	var found Location
	total := 0
	for path, patch := range patches {
		if path == file {
			continue
		}
		code, _ := hunkSides(patch)
		starts := matchStarts(want, code)
		total += len(starts)
		if len(starts) == 1 {
			found = span(want, code, starts[0]).on(path, "new", "other-file")
		}
	}
	return found, total == 1
}

// LocateAt finds a quote in content on lines that include line, trying every occurrence, since a cited
// line such as a closing brace often repeats earlier in the file.
func LocateAt(quote, content string, line int) (Location, bool) {
	want := normalizeQuote(quote)
	if len(want) == 0 {
		return Location{}, false
	}
	code := numberLines(strings.Split(content, "\n"), 1)
	for _, start := range matchStarts(want, code) {
		if loc := span(want, code, start); loc.Start <= line && line <= loc.End {
			return loc, true
		}
	}
	return Location{}, false
}

func (l Location) on(file, side, method string) Location {
	l.File, l.Side, l.Method = file, side, method
	return l
}

// normalizeQuote strips pasted diff markers only when every non-empty line
// carries one, so a quote of real code starting with "-" or "+" is kept intact.
func normalizeQuote(quote string) []string {
	raw := strings.Split(quote, "\n")
	marked := true
	for _, l := range raw {
		if strings.TrimSpace(l) != "" && !strings.ContainsAny(l[:1], "+- ") {
			marked = false
			break
		}
	}
	var out []string
	for _, l := range raw {
		if marked && l != "" {
			l = l[1:]
		}
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func numberLines(lines []string, first int) []codeLine {
	var out []codeLine
	for i, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, codeLine{n: first + i, text: t})
		}
	}
	return out
}

// hunkSides returns the non-blank hunk lines of a unified diff as they exist on
// the new side (head line numbers) and on the old side (base line numbers).
func hunkSides(patch string) (newSide, oldSide []codeLine) {
	var oldN, newN, oldLeft, newLeft int
	for _, l := range strings.Split(patch, "\n") {
		if oldLeft == 0 && newLeft == 0 {
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			oldN, oldLeft = atoi(m[1]), count(m[2])
			newN, newLeft = atoi(m[3]), count(m[4])
			continue
		}
		marker, text := byte(' '), ""
		if l != "" {
			marker, text = l[0], l[1:]
		}
		switch marker {
		case ' ':
			newSide = appendLine(newSide, newN, text)
			oldSide = appendLine(oldSide, oldN, text)
			newN, newLeft, oldN, oldLeft = newN+1, newLeft-1, oldN+1, oldLeft-1
		case '+':
			newSide = appendLine(newSide, newN, text)
			newN, newLeft = newN+1, newLeft-1
		case '-':
			oldSide = appendLine(oldSide, oldN, text)
			oldN, oldLeft = oldN+1, oldLeft-1
		}
	}
	return newSide, oldSide
}

func appendLine(lines []codeLine, n int, text string) []codeLine {
	if t := strings.TrimSpace(text); t != "" {
		lines = append(lines, codeLine{n: n, text: t})
	}
	return lines
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// count reads a hunk range length, which git omits when it is 1.
func count(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func matchStarts(want []string, code []codeLine) []int {
	var starts []int
	for i := 0; i+len(want) <= len(code); i++ {
		if windowEquals(want, code[i:i+len(want)]) {
			starts = append(starts, i)
		}
	}
	return starts
}

func windowEquals(want []string, window []codeLine) bool {
	for j, w := range want {
		if window[j].text != w {
			return false
		}
	}
	return true
}

func firstMatch(want []string, code []codeLine) (Location, bool) {
	starts := matchStarts(want, code)
	if len(starts) == 0 {
		return Location{}, false
	}
	loc := span(want, code, starts[0])
	loc.Ambiguous = len(starts) > 1
	return loc, true
}

func span(want []string, code []codeLine, start int) Location {
	return Location{Start: code[start].n, End: code[start+len(want)-1].n}
}

// Changed is an added (`+`) or removed (`-`) line of a unified diff, numbered on its own side and trimmed.
type Changed struct {
	Side string
	N    int
	Text string
}

// ChangedLines returns the non-blank added and removed lines of a unified diff, in diff order.
func ChangedLines(patch string) []Changed {
	var lines []Changed
	var oldN, newN, oldLeft, newLeft int
	for _, l := range strings.Split(patch, "\n") {
		if oldLeft == 0 && newLeft == 0 {
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				oldN, oldLeft = atoi(m[1]), count(m[2])
				newN, newLeft = atoi(m[3]), count(m[4])
			}
			continue
		}
		marker, text := byte(' '), ""
		if l != "" {
			marker, text = l[0], strings.TrimSpace(l[1:])
		}
		switch marker {
		case ' ':
			newN, newLeft, oldN, oldLeft = newN+1, newLeft-1, oldN+1, oldLeft-1
		case '+':
			if text != "" {
				lines = append(lines, Changed{"+", newN, text})
			}
			newN, newLeft = newN+1, newLeft-1
		case '-':
			if text != "" {
				lines = append(lines, Changed{"-", oldN, text})
			}
			oldN, oldLeft = oldN+1, oldLeft-1
		}
	}
	return lines
}
