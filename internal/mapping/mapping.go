// Package mapping turns a diff into the map's signals. It is pure: data in, data out.
package mapping

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Schema versions the signals JSON; consumers check it before reading anything else.
const Schema = "lupa.signals/v0"

// File is one changed file. Binary files have no line counts.
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	// WhitespaceOnly is the part of Additions and Deletions that only changes whitespace; it is not authored.
	WhitespaceOnly int `json:"-"`
	// FirstLine is the first changed line on the head side, 0 when the file was deleted.
	FirstLine int    `json:"first_line,omitempty"`
	Class     Class  `json:"class"`
	Risk      Risk   `json:"risk"`
	Reason    string `json:"reason"`
	// Importers are the files outside this change that reach this one through imports, directly or not.
	Importers []string `json:"importers,omitempty"`
	// CallSites are the file:line call sites outside the change of the declarations the change touches here.
	CallSites []string `json:"call_sites,omitempty"`
	// History is the file's git history before the change; nil for a new file.
	History *History `json:"history,omitempty"`
	// DirectImporters are the Importers that import this file themselves.
	DirectImporters []string `json:"direct_importers,omitempty"`
	// Owners come from the base branch's CODEOWNERS, so a change cannot pick its own reviewers.
	Owners []string `json:"owners,omitempty"`
}

// History is what the base's last year of git log says about a file, by commit message and author email; a
// fix is a commit whose subject says fix, bug, fault, regression or revert, so the count is approximate.
type History struct {
	FixCommits        int     `json:"fix_commits"`
	Commits90         int     `json:"commits_90d"`
	Authors           int     `json:"authors"`
	TopAuthorShare    float64 `json:"top_author_share"`
	AuthorFirstChange bool    `json:"author_first_change"`
}

// RemovedExport is an exported declaration the change removes or renames, and the importers (file:line)
// outside the change that still import it by name.
type RemovedExport struct {
	File  string   `json:"file"`
	Name  string   `json:"name"`
	Users []string `json:"users"`
}

// Unresolved is an import in a changed file that names a repo file lupa could not find.
type Unresolved struct {
	From      string `json:"from"`
	Specifier string `json:"specifier"`
	Line      int    `json:"line"`
}

// Reach is what the import graph says about the change: importers outside it per changed file,
// those of them that import it directly, and the imports of changed files that could not be resolved.
type Reach struct {
	Importers  map[string][]string
	Direct     map[string][]string
	Unresolved []Unresolved
	// Ignored are imports of paths git ignores: most often generated, so reported apart and never asked about.
	Ignored []Unresolved
	// Imports are the resolved direct imports of changed files; nil when the language is not analyzed.
	Imports map[string][]string
	// Calls are, per changed file whose declarations were analyzed, the call sites (file:line) outside the
	// change of the declarations the change touches; a file absent from it was not analyzed.
	Calls map[string][]string
	// ChangedCallers are, per changed file, the other changed files that call the declarations it changes.
	ChangedCallers map[string][]string
}

// TestMismatch is a test named after a changed file that does not import it, so it may not exercise it.
type TestMismatch struct {
	Test    string `json:"test"`
	Subject string `json:"subject"`
	Line    int    `json:"line"`
}

// Size totals the authored change; generated files are counted apart and add no lines.
type Size struct {
	Files          int `json:"files"`
	Additions      int `json:"additions"`
	Deletions      int `json:"deletions"`
	Changed        int `json:"changed"`
	GeneratedFiles int `json:"generated_files"`
}

// Signals is the map of one change, the shape written by `lupa map --json`.
type Signals struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Files  []File `json:"files"`
	Size   Size   `json:"size"`

	// PR is null for a branch range.
	PR        *PRInfo `json:"pr"`
	ReviewNow Gate    `json:"review_now"`
	// SinceLastLook is null on the first map of a target.
	SinceLastLook *LastLook      `json:"since_last_look"`
	ReadingOrder  []ReadingEntry `json:"reading_order"`
	// Claims is null for a branch range.
	Claims     []Claim      `json:"claims"`
	Unresolved []Unresolved `json:"unresolved"`
	// IgnoredImports name paths the repo's .gitignore covers; omitted when there are none.
	IgnoredImports []Unresolved `json:"ignored_imports,omitempty"`
	// RemovedExports are exported declarations the change removes while files outside it still import them.
	RemovedExports []RemovedExport `json:"removed_exports,omitempty"`
	// SilencedErrors are added lines that swallow an error.
	SilencedErrors []Site `json:"silenced_errors"`
	// NotAnalyzed lists the extensions of changed code files whose imports and calls reach does not follow.
	NotAnalyzed []string `json:"not_analyzed,omitempty"`

	TestsNotImportingSubject []TestMismatch `json:"tests_not_importing_subject"`
}

// LastLook is what changed since the last map of the same target.
type LastLook struct {
	Since string   `json:"since"`
	Files []string `json:"files"`
}

// Decision is whether to review the change now or hand it back to its author.
type Decision string

const (
	Review   Decision = "review"
	HandBack Decision = "hand-back"
)

// Gate is the review-now decision, why, and on a hand-back the reply to suggest to the author.
type Gate struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`
	Reply    string   `json:"reply,omitempty"`
}

// PR is what the forge says about a change that is a pull request: its description, linked issues,
// and the open pull requests of its repo, itself included or not.
type PR struct {
	Number           int
	URL, Title       string
	BaseRef, HeadRef string
	Body             string
	LinkedIssues     int
	Open             []OpenPR
}

// OpenPR is another open pull request and the files it changes.
type OpenPR struct {
	Number           int
	Title            string
	BaseRef, HeadRef string
	Files            []string
}

// PRInfo names the pull request in the signals.
type PRInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// purposeWords is the fewest words of description that state a purpose without a linked issue.
const purposeWords = 10

// SizeBudget is the most changed lines, generated files excluded, a change can have and still be reviewed in one sitting.
const SizeBudget = 400

// Input is everything Build needs: the range, the changed files, what reaches them, and their owners.
type Input struct {
	Base   string
	Head   string
	Files  []File
	Reach  Reach
	Owners map[string][]string
	// PR is nil for a branch range.
	PR *PR
	// Patches are the unified diffs per path; claims are looked up in them.
	Patches map[string]string
	// Generated names files found generated by their content, with the reason; path rules cannot see it.
	Generated map[string]string
	// History holds, per file with commits in the base's last year, its history; new files have none.
	History map[string]History
	// RemovedExports are exported declarations the change deletes or renames, with the files still importing them.
	RemovedExports []RemovedExport
	// Analyzed reports whether reach follows a path's imports and calls; nil leaves NotAnalyzed empty.
	Analyzed func(path string) bool
}

// codeExts are the source extensions whose impact a reviewer would expect followed.
var codeExts = set(".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".go", ".py", ".rb", ".java", ".kt", ".rs", ".php",
	".cs", ".swift", ".scala", ".c", ".cc", ".cpp", ".h", ".hpp")

// IsCode reports a source file by extension; data and prose files, such as translations, churn with every
// change, so their history says little about risk.
func IsCode(p string) bool { return codeExts[path.Ext(p)] }

// Build computes the signals of the change.
func Build(in Input) Signals {
	files, reach := in.Files, in.Reach
	s := Signals{Schema: Schema, Base: in.Base, Head: in.Head, Files: make([]File, len(files)), Unresolved: reach.Unresolved, IgnoredImports: reach.Ignored}
	if s.Unresolved == nil {
		s.Unresolved = []Unresolved{}
	}
	for i, f := range files {
		rule := Classify(f.Path)
		if why, ok := in.Generated[f.Path]; ok && rule.Class != Skip {
			rule = Rule{Skip, NoRisk, why}
		}
		for _, r := range in.RemovedExports {
			if r.File == f.Path && rule.Class != Skip && rule.Risk != OneWayDoor {
				n := len(r.Users)
				rule = Rule{ReadFirst, OneWayDoor, fmt.Sprintf("removes exported %s, still imported by %d %s outside this change", r.Name, n, plural(n, "file", "files"))}
				s.RemovedExports = append(s.RemovedExports, r)
			}
		}
		f.Class, f.Risk, f.Reason = rule.Class, rule.Risk, rule.Reason
		f.Importers = reach.Importers[f.Path]
		f.DirectImporters = reach.Direct[f.Path]
		f.Owners = in.Owners[f.Path]
		if h, ok := in.History[f.Path]; ok {
			f.History = &h
		}
		sites, analyzed := reach.Calls[f.Path]
		f.CallSites = sites
		// A file's importers say little when its changed declarations have callers known by name: those are the impact.
		switch {
		case f.Class != Skim:
		case analyzed && len(sites) > 0:
			f.Class, f.Risk = ReadFirst, Impact
			f.Reason = fmt.Sprintf("%d call %s of its changed declarations outside this change", len(sites), plural(len(sites), "site", "sites"))
		case analyzed && len(reach.ChangedCallers[f.Path]) > 0:
			// The rest of the change is built on this file, so reading it first explains the others.
			n := len(reach.ChangedCallers[f.Path])
			f.Class = ReadFirst
			f.Reason = fmt.Sprintf("its changed declarations are called from %d other changed %s", n, plural(n, "file", "files"))
		case !analyzed && len(f.Importers) > 0:
			f.Class, f.Risk = ReadFirst, Impact
			f.Reason = fmt.Sprintf("imported by %d %s outside this change", len(f.Importers), plural(len(f.Importers), "file", "files"))
		}
		s.Files[i] = f
		if ext := path.Ext(f.Path); f.Class != Skip && codeExts[ext] && in.Analyzed != nil && !in.Analyzed(f.Path) && !slices.Contains(s.NotAnalyzed, ext) {
			s.NotAnalyzed = append(s.NotAnalyzed, ext)
		}
		if f.Class == Skip {
			s.Size.GeneratedFiles++
			continue
		}
		s.Size.Files++
		s.Size.Additions += f.Additions
		s.Size.Deletions += f.Deletions
		s.Size.Changed += f.Additions + f.Deletions
		if !indentationMatters(f.Path) {
			s.Size.Changed -= f.WhitespaceOnly
		}
	}
	if in.PR != nil {
		s.PR = &PRInfo{Number: in.PR.Number, URL: in.PR.URL, Title: in.PR.Title}
		if s.Claims = claims(in.PR.Body, s.Files, in.Patches); s.Claims == nil {
			s.Claims = []Claim{}
		}
	}
	s.ReviewNow = gate(s, in.PR)
	s.SilencedErrors = silencedErrors(s.Files, in.Patches)
	if s.SilencedErrors == nil {
		s.SilencedErrors = []Site{}
	}
	s.ReadingOrder, s.TestsNotImportingSubject = readingOrder(s.Files, reach.Imports)
	return s
}

// gate decides review now or hand back; the first rule that matches wins: a possible duplicate,
// the size, then a missing purpose. The PR rules apply only to a pull request.
func gate(s Signals, pr *PR) Gate {
	if pr != nil {
		if g, ok := duplicate(s.Files, pr); ok {
			return g
		}
	}
	if s.Size.Changed > SizeBudget {
		return Gate{HandBack, fmt.Sprintf("%d changed lines, generated excluded, over %d: suggest a split", s.Size.Changed, SizeBudget),
			fmt.Sprintf("This change has %d changed lines without generated files. Could you split it into smaller PRs?", s.Size.Changed)}
	}
	if pr != nil {
		reply := "Could you say in the description what this change is for, or link its issue?"
		switch words := purposeWordCount(pr.Body); {
		case words == 0:
			return Gate{HandBack, "no description: ask for intent", reply}
		case words < purposeWords && pr.LinkedIssues == 0:
			return Gate{HandBack, "no linked issue and no stated purpose: ask for intent", reply}
		}
	}
	return Gate{Decision: Review, Reason: fmt.Sprintf("%d changed lines, generated excluded, within %d", s.Size.Changed, SizeBudget)}
}

// duplicate finds the first other open PR that changes at least half of this change's files,
// generated files excluded. A PR stacked on this one, or under it, shares its files by design.
func duplicate(files []File, pr *PR) (Gate, bool) {
	var own []string
	for _, f := range files {
		if f.Class != Skip {
			own = append(own, f.Path)
		}
	}
	for _, o := range pr.Open {
		if o.Number == pr.Number || o.HeadRef != "" && o.HeadRef == pr.BaseRef || o.BaseRef != "" && o.BaseRef == pr.HeadRef {
			continue
		}
		shared := 0
		for _, p := range own {
			if slices.Contains(o.Files, p) {
				shared++
			}
		}
		// One shared file says little: in a busy repo a small PR shares it with many unrelated ones.
		if shared >= 2 && 2*shared >= len(own) {
			name := fmt.Sprintf("PR #%d", o.Number)
			if o.Title != "" {
				name += " (" + o.Title + ")"
			}
			return Gate{HandBack, fmt.Sprintf("%s changes %d of the same %d files: possible duplicate", name, shared, len(own)),
				fmt.Sprintf("PR #%d changes the same files. Is this one a duplicate, or how do the two relate?", o.Number)}, true
		}
	}
	return Gate{}, false
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	boilerplate = regexp.MustCompile(`^\s*(#|[-*]\s*\[[ xX]\]|$)`)
)

// purposeWordCount counts the description's words outside HTML comments, headings and checkboxes,
// which PR templates leave behind when nobody writes a description.
func purposeWordCount(body string) int {
	n := 0
	for _, line := range strings.Split(htmlComment.ReplaceAllString(body, ""), "\n") {
		if !boilerplate.MatchString(line) {
			n += len(strings.Fields(line))
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// indentationMatters reports a file whose whitespace changes what runs, so whitespace-only lines are real changes.
func indentationMatters(p string) bool {
	switch ext := strings.ToLower(path.Ext(p)); {
	case ext == ".py" || ext == ".yml" || ext == ".yaml":
		return true
	}
	return strings.EqualFold(path.Base(p), "makefile")
}
