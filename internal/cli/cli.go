// Package cli parses lupa's command line and writes its output.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/agent/claude"
	"github.com/pavp/lupa/internal/git"
	"github.com/pavp/lupa/internal/lang/typescript"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/owners"
	"github.com/pavp/lupa/internal/reach"
	"github.com/pavp/lupa/internal/report"
	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/store"
)

const usage = "usage: lupa map <base>..<head> | <branch> [--json]\n       lupa review <base>..<head> | <branch> [--agent claude] [--plan] [--yes] [--json]"

// Env is what a command reads and writes: the working directory, the streams, and the agent,
// which is the claude adapter when nil.
type Env struct {
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Agent  agent.Agent
}

// usageError is reported with the usage line and exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// Run executes one lupa command and returns its exit code: 0 on success, 2 on a usage or input error.
func Run(args []string, env Env) int {
	if err := run(args, env); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "lupa: %s\n%s\n", ue.msg, usage)
		} else {
			fmt.Fprintf(env.Stderr, "lupa: %s\n", err)
		}
		return 2
	}
	return 0
}

func run(args []string, env Env) error {
	if len(args) == 0 {
		return usageError{"missing command"}
	}
	switch args[0] {
	case "map":
		return runMap(args[1:], env)
	case "review":
		return runReview(args[1:], env)
	default:
		return usageError{fmt.Sprintf("unknown command %q", args[0])}
	}
}

// parseArgs reads one target, the given boolean flags and the given flags that take a value;
// any other flag or argument is a usage error.
func parseArgs(args []string, flags map[string]*bool, values map[string]*string) (target string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case flags[a] != nil:
			*flags[a] = true
		case values[a] != nil:
			if i+1 == len(args) {
				return "", usageError{fmt.Sprintf("flag %q needs a value", a)}
			}
			i++
			*values[a] = args[i]
		case strings.HasPrefix(a, "-"):
			return "", usageError{fmt.Sprintf("unknown flag %q", a)}
		case target != "":
			return "", usageError{fmt.Sprintf("unexpected argument %q", a)}
		default:
			target = a
		}
	}
	if target == "" {
		return "", usageError{"missing target"}
	}
	return target, nil
}

// mapChange resolves the target to a commit range and builds the map's signals for it.
func mapChange(env Env, target string) (git.Repo, mapping.Signals, error) {
	repo := git.Repo{Dir: env.Dir}
	if err := repo.Check(); err != nil {
		return repo, mapping.Signals{}, fmt.Errorf("%w: %s", err, env.Dir)
	}
	base, head, err := resolveRange(repo, target)
	if err != nil {
		return repo, mapping.Signals{}, err
	}
	changes, err := repo.NumStat(base, head)
	if err != nil {
		return repo, mapping.Signals{}, err
	}
	firstLines, err := repo.FirstChangedLines(base, head)
	if err != nil {
		return repo, mapping.Signals{}, err
	}
	files := make([]mapping.File, len(changes))
	for i, c := range changes {
		files[i] = mapping.File{Path: c.Path, Additions: c.Additions, Deletions: c.Deletions, Binary: c.Binary, FirstLine: firstLines[c.Path]}
	}
	r, err := reachOf(repo, head, files)
	if err != nil {
		return repo, mapping.Signals{}, err
	}
	return repo, mapping.Build(mapping.Input{Base: base, Head: head, Files: files, Reach: r, Owners: ownersOf(repo, base, files)}), nil
}

func runMap(args []string, env Env) error {
	asJSON := false
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON}, nil)
	if err != nil {
		return err
	}
	repo, signals, err := mapChange(env, target)
	if err != nil {
		return err
	}
	head := signals.Head
	gitDir, err := repo.GitDir()
	if err != nil {
		return err
	}
	st := store.Store{Root: filepath.Join(gitDir, "lupa")}
	signals.SinceLastLook = lastLook(repo, st, target, head)
	saved, err := json.MarshalIndent(signals, "", "  ")
	if err != nil {
		return err
	}
	saved = append(saved, '\n')
	md := report.Markdown(signals)
	dir, err := st.Save(target, head, saved, md)
	if err != nil {
		return fmt.Errorf("saving the map: %w", err)
	}

	if asJSON {
		_, err = env.Stdout.Write(saved)
		return err
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\nSaved: %s · next time: only what changed since %s\n", md, dir, head[:7])
	return err
}

// planJSON is the shape written by `lupa review --plan --json`.
type planJSON struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	review.Result
}

// reviewJSON is the shape of `lupa review --json` and of the saved review.json.
type reviewJSON struct {
	Schema   string         `json:"schema"`
	Base     string         `json:"base"`
	Head     string         `json:"head"`
	Findings []findingJSON  `json:"findings"`
	Refuted  []refutedJSON  `json:"refuted"`
	Dropped  []droppedJSON  `json:"dropped"`
	Ledger   []review.Entry `json:"ledger"`
	Usage    usageJSON      `json:"usage"`
}

type findingJSON struct {
	File      string `json:"file"`
	Side      string `json:"side"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Severity  string `json:"severity"`
	Level     string `json:"level"`
	Causality string `json:"causality"`
	Claim     string `json:"claim"`
	Mechanism string `json:"mechanism"`
	Fix       string `json:"fix"`
	Quote     string `json:"quote"`
	Relocated bool   `json:"relocated"`
	Merged    int    `json:"merged"`
}

type refutedJSON struct {
	Finding  findingJSON     `json:"finding"`
	Citation review.Citation `json:"citation"`
}

type droppedJSON struct {
	Stage  string `json:"stage"`
	Group  int    `json:"group"`
	Reason string `json:"reason"`
	File   string `json:"file"`
	Claim  string `json:"claim,omitempty"`
	Quote  string `json:"quote,omitempty"`
}

type usageJSON struct {
	Calls        int     `json:"calls"`
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

func reviewJSONOf(base, head string, out review.RunOutput) reviewJSON {
	r := reviewJSON{Schema: "lupa.review/v0", Base: base, Head: head, Findings: []findingJSON{}, Refuted: []refutedJSON{},
		Dropped: []droppedJSON{}, Ledger: out.Ledger, Usage: usageJSON(out.Usage)}
	for _, f := range out.Findings {
		r.Findings = append(r.Findings, findingJSONOf(f))
	}
	for _, f := range out.Refuted {
		citation := f.Citation
		citation.Quote = report.Redact(citation.Quote)
		r.Refuted = append(r.Refuted, refutedJSON{Finding: findingJSONOf(f.Finding), Citation: citation})
	}
	for _, d := range out.Dropped {
		r.Dropped = append(r.Dropped, droppedJSON{Stage: d.Stage, Group: d.Group, Reason: d.Reason, File: d.Comment.File,
			Claim: report.Redact(d.Comment.Claim), Quote: report.Redact(d.Comment.Quote)})
	}
	if r.Ledger == nil {
		r.Ledger = []review.Entry{}
	}
	return r
}

func findingJSONOf(a review.Anchored) findingJSON {
	f, c := a.Finding, a.Finding.Comment
	return findingJSON{File: f.Location.File, Side: f.Location.Side, Start: f.Location.Start, End: f.Location.End, Severity: c.Severity,
		Level: f.Level, Causality: f.Causality, Claim: report.Redact(c.Claim), Mechanism: report.Redact(c.Mechanism), Fix: report.Redact(c.Fix),
		Quote: report.Redact(c.Quote), Relocated: a.Relocated, Merged: f.Merged}
}

func runReview(args []string, env Env) error {
	asJSON, planOnly, yes, agentName := false, false, false, "claude"
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON, "--plan": &planOnly, "--yes": &yes}, map[string]*string{"--agent": &agentName})
	if err != nil {
		return err
	}
	if agentName != "claude" {
		return usageError{fmt.Sprintf("unknown agent %q", agentName)}
	}
	repo, signals, err := mapChange(env, target)
	if err != nil {
		return err
	}
	patches, err := repo.Patches(signals.Base, signals.Head)
	if err != nil {
		return err
	}
	conventions := conventionsOf(repo, signals.Base)
	empty := review.BuildPrompt(review.PromptInput{Conventions: conventions})
	plan := review.Plan(signals.Files, patches, review.Options{Overhead: len(empty.System) + len(empty.User)})
	if planOnly && asJSON {
		out, err := json.MarshalIndent(planJSON{Schema: "lupa.plan/v0", Base: signals.Base, Head: signals.Head, Result: plan}, "", "  ")
		if err != nil {
			return err
		}
		_, err = env.Stdout.Write(append(out, '\n'))
		return err
	}
	if !asJSON {
		if _, err := io.WriteString(env.Stdout, planText(plan, target, !yes)); err != nil {
			return err
		}
	}
	if planOnly {
		return nil
	}
	// With --json, stdout carries only the JSON, so the question goes to stderr.
	talk := env.Stdout
	if asJSON {
		talk = env.Stderr
	}
	if !yes && !confirm(env.Stdin, talk) {
		_, err := fmt.Fprintf(talk, "Not run. Read the change first: lupa map %s\n", target)
		return err
	}

	ag := env.Agent
	if ag == nil {
		ag = claude.Adapter{}
	}
	importers := map[string][]string{}
	for _, f := range signals.Files {
		if len(f.Importers) > 0 {
			importers[f.Path] = f.Importers
		}
	}
	out, err := agentReview(repo, signals.Head, review.RunInput{Plan: plan, Patches: patches, Importers: importers, Conventions: conventions, Agent: ag})
	if err != nil {
		return err
	}
	saved, err := json.MarshalIndent(reviewJSONOf(signals.Base, signals.Head, out), "", "  ")
	if err != nil {
		return err
	}
	saved = append(saved, '\n')
	gitDir, err := repo.GitDir()
	if err != nil {
		return err
	}
	st := store.Store{Root: filepath.Join(gitDir, "lupa")}
	path, err := st.Put(target, signals.Head, "review.json", saved)
	if err != nil {
		return fmt.Errorf("saving the review: %w", err)
	}
	if asJSON {
		_, err = env.Stdout.Write(saved)
		return err
	}
	md := report.Review(report.ReviewInput{Base: signals.Base, Head: signals.Head, Out: out})
	if _, err := st.Put(target, signals.Head, "review.md", md); err != nil {
		return fmt.Errorf("saving the review: %w", err)
	}
	_, err = fmt.Fprintf(env.Stdout, "%s\nSaved: %s\n", md, filepath.Dir(path))
	return err
}

// confirm asks whether to run the agent review now; only y or yes runs it, and end of input does not.
func confirm(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "Run the agent review now? [y/N] ")
	if in == nil {
		return false
	}
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// conventionsOf reads the repo's instruction files at the base, so a change cannot instruct its own reviewer.
func conventionsOf(repo git.Repo, base string) []review.Convention {
	var out []review.Convention
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if src, ok := repo.Show(base, name); ok {
			out = append(out, review.Convention{Path: name, Content: string(src)})
		}
	}
	return out
}

// agentReview runs the review in a detached worktree at head, which it always removes.
func agentReview(repo git.Repo, head string, in review.RunInput) (out review.RunOutput, err error) {
	dir, remove, err := repo.Worktree(head)
	if err != nil {
		return out, fmt.Errorf("creating the review worktree: %w", err)
	}
	defer func() { err = errors.Join(err, remove()) }()
	blobs, err := repo.Reader(head)
	if err != nil {
		return out, err
	}
	defer blobs.Close()
	in.Dir = dir
	in.Head = func(path string) (string, bool) {
		b, err := blobs.Read(path)
		return string(b), err == nil
	}
	return review.Run(context.Background(), in), nil
}

// planText renders the plan; hint adds how to read the change first, for a user who is asked before the run.
func planText(p review.Result, target string, hint bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %d %s, %d %s, about %d tokens (estimate)\n", len(p.Groups), plural(len(p.Groups), "group", "groups"),
		p.Estimate.Runs, plural(p.Estimate.Runs, "agent run", "agent runs"), p.Estimate.Tokens)
	for _, g := range p.Groups {
		fmt.Fprintf(&b, "\nGroup %d (%d bytes)\n", g.ID, g.Bytes)
		for _, part := range g.Parts {
			fmt.Fprintf(&b, "  %s", part.Path)
			if len(part.Hunks) > 0 {
				lines := make([]string, len(part.Hunks))
				for i, h := range part.Hunks {
					lines[i] = strconv.Itoa(h)
				}
				fmt.Fprintf(&b, " (hunks at lines %s)", strings.Join(lines, ", "))
			}
			b.WriteString("\n")
		}
	}
	for _, s := range []struct {
		status review.Status
		title  string
	}{{review.Skipped, "Skipped"}, {review.NotReviewed, "Not reviewed"}} {
		header := false
		for _, e := range p.Ledger {
			if e.Status != s.status {
				continue
			}
			if !header {
				fmt.Fprintf(&b, "\n%s:\n", s.title)
				header = true
			}
			fmt.Fprintf(&b, "  %s: %s\n", e.Path, e.Reason)
		}
	}
	if hint {
		fmt.Fprintf(&b, "\nRead the change first (recommended): lupa map %s\nThen run the agent review: lupa review %s --yes\n", target, target)
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reachOf builds the import graph at head and keeps, per changed file, its importers outside the change
// and the imports of changed files that do not resolve. It reads nothing when no changed file is TS/JS.
func reachOf(repo git.Repo, head string, changed []mapping.File) (mapping.Reach, error) {
	provider := typescript.Provider{}
	inChange := map[string]bool{}
	owned := false
	for _, f := range changed {
		inChange[f.Path] = true
		owned = owned || provider.Owns(f.Path)
	}
	if !owned {
		return mapping.Reach{}, nil
	}
	all, err := repo.Files(head)
	if err != nil {
		return mapping.Reach{}, err
	}
	blobs, err := repo.Reader(head)
	if err != nil {
		return mapping.Reach{}, err
	}
	defer blobs.Close()
	g, err := reach.Build(all, blobs.Read, provider)
	if err != nil {
		return mapping.Reach{}, fmt.Errorf("reach: %w", err)
	}
	out := mapping.Reach{Importers: map[string][]string{}, Imports: map[string][]string{}}
	for _, f := range changed {
		if imps, ok := g.Imports[f.Path]; ok {
			out.Imports[f.Path] = imps
		}
		for _, imp := range g.Importers(f.Path) {
			if !inChange[imp] {
				out.Importers[f.Path] = append(out.Importers[f.Path], imp)
			}
		}
	}
	for _, u := range g.Unresolved {
		if inChange[u.From] {
			out.Unresolved = append(out.Unresolved, mapping.Unresolved{From: u.From, Specifier: u.Specifier, Line: u.Line})
		}
	}
	return out, nil
}

// lastLook compares head with the last mapped head of the same target; nil on a first map,
// or when that head no longer exists (garbage-collected after a force push).
func lastLook(repo git.Repo, st store.Store, target, head string) *mapping.LastLook {
	last, ok := st.Last(target)
	if !ok {
		return nil
	}
	changes, err := repo.NumStat(last, head)
	if err != nil {
		return nil
	}
	ll := &mapping.LastLook{Since: last, Files: []string{}}
	for _, c := range changes {
		ll.Files = append(ll.Files, c.Path)
	}
	return ll
}

// ownersOf reads CODEOWNERS at the base, in GitHub's lookup order, so a change cannot assign its own owners.
func ownersOf(repo git.Repo, base string, files []mapping.File) map[string][]string {
	for _, loc := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		src, ok := repo.Show(base, loc)
		if !ok {
			continue
		}
		co := owners.Parse(src)
		out := map[string][]string{}
		for _, f := range files {
			if o := co.Owners(f.Path); o != nil {
				out[f.Path] = o
			}
		}
		return out
	}
	return nil
}

// resolveRange turns `<from>..<to>` or `<branch>` into the merge-base and head commits, the diff a PR would show.
func resolveRange(repo git.Repo, target string) (base, head string, err error) {
	from, to, isRange := strings.Cut(target, "..")
	if !isRange {
		if from, err = repo.DefaultBranch(); err != nil {
			return "", "", err
		}
		to = target
	}
	to = strings.TrimPrefix(to, ".")
	fromCommit, err := repo.Commit(from)
	if err != nil {
		return "", "", err
	}
	if head, err = repo.Commit(to); err != nil {
		return "", "", err
	}
	if base, err = repo.MergeBase(fromCommit, head); err != nil {
		return "", "", err
	}
	return base, head, nil
}
