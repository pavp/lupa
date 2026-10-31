// Package cli parses lupa's command line and writes its output.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/agent/claude"
	"github.com/pavp/lupa/internal/agent/pi"
	"github.com/pavp/lupa/internal/forge/github"
	"github.com/pavp/lupa/internal/git"
	"github.com/pavp/lupa/internal/lang"
	"github.com/pavp/lupa/internal/lang/typescript"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/owners"
	"github.com/pavp/lupa/internal/reach"
	"github.com/pavp/lupa/internal/report"
	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/store"
	"github.com/pavp/lupa/internal/verify"
)

const usage = "usage: lupa map <pr> | <base>..<head> | <branch> [--json]\n       lupa review <pr> | <base>..<head> | <branch> [--agent claude|pi] [--model <id>] [--effort <level>] [--orchestrate] [--investigate] [--pi-extensions <path,...>] [--plan] [--yes] [--json]\n<pr> is a pull request number (482, #482) or URL, read through gh"

// Env is what a command reads and writes: the working directory, the streams, the agent, which
// is the claude adapter when nil, and the forge.
type Env struct {
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Agent  agent.Agent
	// Forge reads pull requests; the GitHub client through gh when nil.
	Forge Forge
	// CacheDir holds the clones of PRs reviewed outside their clone; empty means lupa under the user cache dir.
	CacheDir string
}

// Forge reads a pull request, running in dir so a bare number resolves against that clone, and
// lists the open pull requests of a repo.
type Forge interface {
	PR(dir string, ref github.Ref) (github.PR, error)
	OpenPRs(ref github.Ref) ([]github.PR, error)
}

// usageError is reported with the usage line and exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// errNothingReviewed is reported after the review is saved and shown, with exit code 1.
var errNothingReviewed = errors.New("no group was reviewed: every agent run failed or answered unusably; see the coverage")

// Run executes one lupa command and returns its exit code: 0 on success, 1 when a review ran but
// no group could be reviewed, 2 on a usage or input error.
func Run(args []string, env Env) int {
	if err := run(args, env); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(env.Stderr, "lupa: %s\n%s\n", ue.msg, usage)
		} else {
			fmt.Fprintf(env.Stderr, "lupa: %s\n", err)
		}
		if errors.Is(err, errNothingReviewed) {
			return 1
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

// mapChange resolves the target to a commit range and builds the map's signals for it. key names
// the target in the store: the target itself, or pr-<n> for a pull request in any of its forms.
func mapChange(env Env, target string) (m mapped, err error) {
	t, err := resolveTarget(env, target)
	m.repo, m.key = t.repo, t.key
	repo, base, head := t.repo, t.base, t.head
	if err != nil {
		return m, err
	}
	changes, err := repo.NumStat(base, head)
	if err != nil {
		return m, err
	}
	firstLines, err := repo.FirstChangedLines(base, head)
	if err != nil {
		return m, err
	}
	files := make([]mapping.File, len(changes))
	for i, c := range changes {
		files[i] = mapping.File{Path: c.Path, OldPath: c.OldPath, Additions: c.Additions, Deletions: c.Deletions, Binary: c.Binary, FirstLine: firstLines[c.Path],
			WhitespaceOnly: max(0, c.Additions+c.Deletions-c.Authored)}
	}
	view, closeView, err := openHead(repo, head, files)
	if err != nil {
		return m, err
	}
	defer closeView()
	r := reachOf(view, files)
	if m.patches, err = repo.Patches(base, head); err != nil {
		return m, err
	}
	if m.symbols, err = symbolsOf(view, m.patches); err != nil {
		return m, err
	}
	r.Calls, r.ChangedCallers = callSitesOf(m.symbols, files)
	m.signals = mapping.Build(mapping.Input{Base: base, Head: head, Files: files, Reach: r, Owners: ownersOf(repo, base, files), PR: prOf(env, t), Patches: m.patches})
	return m, nil
}

// mapped is a mapped change: the repo and store key, the map's signals, and the patches and changed
// declarations a review reuses instead of reading them again.
type mapped struct {
	repo    git.Repo
	key     string
	signals mapping.Signals
	patches map[string]string
	symbols map[string][]reach.Symbol
}

// headView is the head commit as the map reads it, opened once per run: its files, a blob reader, and the
// resolver and import graph of the analyzed language.
type headView struct {
	all      []string
	read     func(string) ([]byte, error)
	resolver lang.Resolver
	graph    reach.Graph
}

// openHead builds the head view when the change touches an analyzed language; nil otherwise.
func openHead(repo git.Repo, head string, files []mapping.File) (*headView, func() error, error) {
	provider := typescript.Provider{}
	if !slices.ContainsFunc(files, func(f mapping.File) bool { return provider.Owns(f.Path) }) {
		return nil, func() error { return nil }, nil
	}
	all, err := repo.Files(head)
	if err != nil {
		return nil, nil, err
	}
	blobs, err := repo.Reader(head)
	if err != nil {
		return nil, nil, err
	}
	resolver, err := provider.NewResolver(all, blobs.Read)
	if err != nil {
		blobs.Close()
		return nil, nil, fmt.Errorf("reach: %w", err)
	}
	g, err := reach.BuildWith(all, blobs.Read, provider, resolver)
	if err != nil {
		blobs.Close()
		return nil, nil, fmt.Errorf("reach: %w", err)
	}
	return &headView{all: all, read: blobs.Read, resolver: resolver, graph: g}, blobs.Close, nil
}

func runMap(args []string, env Env) error {
	asJSON := false
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON}, nil)
	if err != nil {
		return err
	}
	m, err := mapChange(env, target)
	if err != nil {
		return err
	}
	repo, signals, key := m.repo, m.signals, m.key
	head := signals.Head
	gitDir, err := repo.GitDir()
	if err != nil {
		return err
	}
	st := store.Store{Root: filepath.Join(gitDir, "lupa")}
	signals.SinceLastLook = lastLook(repo, st, key, head)
	saved, err := json.MarshalIndent(signals, "", "  ")
	if err != nil {
		return err
	}
	saved = append(saved, '\n')
	md := report.Markdown(signals)
	dir, err := st.Save(key, head, saved, md)
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
	Schema      string            `json:"schema"`
	Base        string            `json:"base"`
	Head        string            `json:"head"`
	Findings    []findingJSON     `json:"findings"`
	Refuted     []refutedJSON     `json:"refuted"`
	Unconfirmed []unconfirmedJSON `json:"unconfirmed"`
	Dropped     []droppedJSON     `json:"dropped"`
	Ledger      []review.Entry    `json:"ledger"`
	Usage       usageJSON         `json:"usage"`
	Notes       []string          `json:"notes"`
	Claims      []claimJSON       `json:"claims"`
}

type claimJSON struct {
	Sentence string `json:"sentence"`
	Claim    string `json:"claim"`
	Outcome  string `json:"outcome"`
	File     string `json:"file,omitempty"`
	Side     string `json:"side,omitempty"`
	Line     int    `json:"line,omitempty"`
	Code     string `json:"code,omitempty"`
}

type findingJSON struct {
	File      string            `json:"file"`
	Side      string            `json:"side"`
	Start     int               `json:"start"`
	End       int               `json:"end"`
	Severity  string            `json:"severity"`
	Harm      string            `json:"harm,omitempty"`
	Held      string            `json:"not_promoted,omitempty"`
	Level     string            `json:"level"`
	Causality string            `json:"causality"`
	Claim     string            `json:"claim"`
	Mechanism string            `json:"mechanism"`
	Fix       string            `json:"fix"`
	Quote     string            `json:"quote"`
	Relocated bool              `json:"relocated"`
	Merged    int               `json:"merged"`
	Evidence  []review.Evidence `json:"evidence,omitempty"`
}

type refutedJSON struct {
	Finding  findingJSON       `json:"finding"`
	Evidence []review.Evidence `json:"evidence"`
	Reason   string            `json:"reason,omitempty"`
}

type unconfirmedJSON struct {
	Finding findingJSON `json:"finding"`
	Reason  string      `json:"reason"`
}

type droppedJSON struct {
	Stage  string `json:"stage"`
	Group  int    `json:"group"`
	Reason string `json:"reason"`
	File   string `json:"file"`
	Claim  string `json:"claim,omitempty"`
	Quote  string `json:"quote,omitempty"`
}

type tallyJSON struct {
	Calls            int     `json:"calls"`
	CostUSD          float64 `json:"cost_usd"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
}

type usageJSON struct {
	tallyJSON
	Stages map[string]tallyJSON `json:"stages"`
}

func usageJSONOf(u review.Usage) usageJSON {
	r := usageJSON{tallyJSON: tallyJSON(u.Tally), Stages: map[string]tallyJSON{}}
	for k, t := range u.Stages {
		r.Stages[k] = tallyJSON(t)
	}
	return r
}

func reviewJSONOf(base, head string, out review.RunOutput) reviewJSON {
	r := reviewJSON{Schema: "lupa.review/v0", Base: base, Head: head, Findings: []findingJSON{}, Refuted: []refutedJSON{},
		Unconfirmed: []unconfirmedJSON{}, Dropped: []droppedJSON{}, Ledger: []review.Entry{}, Usage: usageJSONOf(out.Usage), Notes: []string{}}
	for _, e := range out.Ledger {
		e.Reason = report.Redact(e.Reason)
		r.Ledger = append(r.Ledger, e)
	}
	for _, n := range out.Notes {
		r.Notes = append(r.Notes, report.Redact(n))
	}
	for _, f := range out.Findings {
		r.Findings = append(r.Findings, findingJSONOf(f))
	}
	for _, c := range out.Claims {
		r.Claims = append(r.Claims, claimJSON{Sentence: report.Redact(c.Sentence), Claim: report.Redact(c.Claim), Outcome: c.Outcome,
			File: c.File, Side: c.Side, Line: c.Line, Code: report.Redact(c.Code)})
	}
	for _, f := range out.Refuted {
		r.Refuted = append(r.Refuted, refutedJSON{Finding: findingJSONOf(f.Finding), Evidence: evidenceJSON(f.Evidence), Reason: report.Redact(f.Reason)})
	}
	for _, f := range out.Unconfirmed {
		r.Unconfirmed = append(r.Unconfirmed, unconfirmedJSON{Finding: findingJSONOf(f.Finding), Reason: report.Redact(f.Reason)})
	}
	for _, d := range out.Dropped {
		r.Dropped = append(r.Dropped, droppedJSON{Stage: d.Stage, Group: d.Group, Reason: report.Redact(d.Reason), File: d.Comment.File,
			Claim: report.Redact(d.Comment.Claim), Quote: report.Redact(d.Comment.Quote)})
	}
	return r
}

func findingJSONOf(a review.Anchored) findingJSON {
	f, c := a.Finding, a.Finding.Comment
	return findingJSON{File: f.Location.File, Side: f.Location.Side, Start: f.Location.Start, End: f.Location.End, Severity: c.Severity, Harm: c.Harm,
		Level: f.Level, Causality: f.Causality, Claim: report.Redact(c.Claim), Mechanism: report.Redact(c.Mechanism), Fix: report.Redact(c.Fix),
		Quote: report.Redact(c.Quote), Relocated: a.Relocated, Merged: f.Merged, Evidence: evidenceJSON(a.Evidence), Held: report.Redact(a.Held)}
}

func evidenceJSON(es []review.Evidence) []review.Evidence {
	var out []review.Evidence
	for _, e := range es {
		e.Quote = report.Redact(e.Quote)
		out = append(out, e)
	}
	return out
}

// reviewFlags are the flags that choose how a review runs.
type reviewFlags struct {
	agent, model, extensions, effort string
	orchestrate, investigate         bool
}

// agentFlagsOf repeats the flags a review was given, quoted for a shell, so a suggested command runs the same review.
func agentFlagsOf(r reviewFlags) string {
	var b strings.Builder
	for _, f := range [][2]string{{"--agent", r.agent}, {"--model", r.model}, {"--pi-extensions", r.extensions}, {"--effort", r.effort}} {
		if f[1] == "" || (f[0] == "--agent" && f[1] == "claude") {
			continue
		}
		v := f[1]
		if strings.ContainsAny(v, " \t'\"$`\\") {
			v = "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
		}
		fmt.Fprintf(&b, " %s %s", f[0], v)
	}
	if r.orchestrate {
		b.WriteString(" --orchestrate")
	}
	if r.investigate {
		b.WriteString(" --investigate")
	}
	return b.String()
}

// agentOf builds the named agent's adapter; extensions is a comma-separated list of Pi extension paths.
func agentOf(name, model, extensions, effort string) (agent.Agent, error) {
	switch name {
	case "claude":
		return claude.Adapter{Model: model, Effort: effort}, nil
	case "pi":
		var exts []string
		for _, e := range strings.Split(extensions, ",") {
			if e = strings.TrimSpace(e); e != "" {
				exts = append(exts, e)
			}
		}
		return pi.Adapter{Model: model, Extensions: exts}, nil
	}
	return nil, usageError{fmt.Sprintf("unknown agent %q", name)}
}

func runReview(args []string, env Env) error {
	asJSON, planOnly, yes := false, false, false
	fl := reviewFlags{agent: "claude"}
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON, "--plan": &planOnly, "--yes": &yes, "--orchestrate": &fl.orchestrate, "--investigate": &fl.investigate},
		map[string]*string{"--agent": &fl.agent, "--model": &fl.model, "--pi-extensions": &fl.extensions, "--effort": &fl.effort})
	if err != nil {
		return err
	}
	agentName := fl.agent
	switch {
	case fl.extensions != "" && agentName != "pi":
		return usageError{"--pi-extensions needs --agent pi"}
	case fl.effort != "" && agentName != "claude":
		return usageError{"--effort needs --agent claude"}
	case fl.orchestrate && agentName != "claude":
		return usageError{"--orchestrate needs --agent claude"}
	}
	// Pi keeps the investigator it always had; for Claude Code it is opt-in.
	investigate := fl.investigate || agentName == "pi"
	adapter, err := agentOf(agentName, fl.model, fl.extensions, fl.effort)
	if err != nil {
		return err
	}
	m, err := mapChange(env, target)
	if err != nil {
		return err
	}
	repo, signals, key, patches := m.repo, m.signals, m.key, m.patches
	conventions := conventionsOf(repo, signals.Base)
	empty := review.BuildPrompt(review.PromptInput{Conventions: conventions})
	plan := review.Plan(signals.Files, patches, review.Options{Overhead: len(empty.System) + len(empty.User)})
	// One session reviews and validates every group with Claude Code subagents; otherwise one run per group, plus
	// the investigators when asked for.
	orchestrate := fl.orchestrate
	switch {
	case len(plan.Groups) == 0:
	case orchestrate:
		plan.Estimate.Runs = 1
	case !investigate:
		plan.Estimate.Runs = len(plan.Groups)
	}
	sentences, claimPatches := uncheckedClaims(signals, patches)
	if len(sentences) > 0 {
		plan.Estimate.Runs += review.ClaimRuns(claimPatches)
		for _, p := range claimPatches {
			plan.Estimate.Tokens += len(p) / 4
		}
	}
	if planOnly && asJSON {
		out, err := json.MarshalIndent(planJSON{Schema: "lupa.plan/v0", Base: signals.Base, Head: signals.Head, Result: plan}, "", "  ")
		if err != nil {
			return err
		}
		_, err = env.Stdout.Write(append(out, '\n'))
		return err
	}
	if !asJSON {
		if _, err := io.WriteString(env.Stdout, planText(plan, target, agentFlagsOf(fl), !yes)); err != nil {
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
		ag = adapter
	}
	importers := map[string][]string{}
	for _, f := range signals.Files {
		if len(f.DirectImporters) > 0 {
			importers[f.Path] = f.DirectImporters
		}
	}
	symbols := m.symbols
	out, err := agentReview(repo, signals.Head, review.RunInput{Plan: plan, Patches: patches, Importers: importers, Symbols: symbols, Conventions: conventions, Agent: ag, Orchestrate: orchestrate, Investigate: investigate,
		Sentences: sentences, ClaimPatches: claimPatches})
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
	path, err := st.Put(key, signals.Head, "review.json", saved)
	if err != nil {
		return fmt.Errorf("saving the review: %w", err)
	}
	if asJSON {
		_, err = env.Stdout.Write(saved)
	} else {
		md := report.Review(report.ReviewInput{Base: signals.Base, Head: signals.Head, Out: out, Map: &signals})
		if _, err := st.Put(key, signals.Head, "review.md", md); err != nil {
			return fmt.Errorf("saving the review: %w", err)
		}
		_, err = fmt.Fprintf(env.Stdout, "\n%s\nSaved: %s\n", md, filepath.Dir(path))
	}
	if err == nil && len(plan.Groups) > 0 && !slices.ContainsFunc(out.Ledger, func(e review.Entry) bool { return e.Status == review.Reviewed }) {
		err = errNothingReviewed
	}
	return err
}

// uncheckedClaims returns the description sentences a search could not settle, and the patches of the
// files they may cite: every changed file but the generated ones.
func uncheckedClaims(signals mapping.Signals, patches map[string]string) ([]string, map[string]string) {
	var sentences []string
	for _, c := range signals.Claims {
		if c.Unchecked() {
			sentences = append(sentences, c.Text)
		}
	}
	if len(sentences) == 0 {
		return nil, nil
	}
	cited := map[string]string{}
	for _, f := range signals.Files {
		if p, ok := patches[f.Path]; ok && f.Class != mapping.Skip {
			cited[f.Path] = p
		}
	}
	return sentences, cited
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

// planText renders the plan; hint adds how to read the change first, for a user who is asked before the run,
// and the review command to run then, with the agent flags of this one.
func planText(p review.Result, target, agentFlags string, hint bool) string {
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
		fmt.Fprintf(&b, "\nRead the change first (recommended): lupa map %s\nThen run the agent review: lupa review %s --yes%s\n", target, target, agentFlags)
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
func reachOf(view *headView, changed []mapping.File) mapping.Reach {
	if view == nil {
		return mapping.Reach{}
	}
	inChange := map[string]bool{}
	for _, f := range changed {
		inChange[f.Path] = true
	}
	g := view.graph
	out := mapping.Reach{Importers: map[string][]string{}, Direct: map[string][]string{}, Imports: map[string][]string{}}
	for _, f := range changed {
		if imps, ok := g.Imports[f.Path]; ok {
			out.Imports[f.Path] = imps
		}
		for _, imp := range g.Importers(f.Path) {
			if !inChange[imp] {
				out.Importers[f.Path] = append(out.Importers[f.Path], imp)
			}
		}
		for _, imp := range g.DirectImporters(f.Path) {
			if !inChange[imp] {
				out.Direct[f.Path] = append(out.Direct[f.Path], imp)
			}
		}
	}
	for _, u := range g.Unresolved {
		if inChange[u.From] {
			out.Unresolved = append(out.Unresolved, mapping.Unresolved{From: u.From, Specifier: u.Specifier, Line: u.Line})
		}
	}
	for _, u := range g.Ignored {
		if inChange[u.From] {
			out.Ignored = append(out.Ignored, mapping.Unresolved{From: u.From, Specifier: u.Specifier, Line: u.Line})
		}
	}
	return out
}

// symbolsOf finds, at head, the TS/JS declarations the patches add lines to, with their callers and callees,
// by path. It reads nothing when no patch adds a line to a TS/JS file.
func symbolsOf(view *headView, patches map[string]string) (map[string][]reach.Symbol, error) {
	provider := typescript.Provider{}
	added := map[string][]int{}
	for path, patch := range patches {
		if lines := verify.AddedLines(patch); provider.Owns(path) && len(lines) > 0 {
			added[path] = lines
		}
	}
	if len(added) == 0 || view == nil {
		return nil, nil
	}
	syms, err := reach.ChangedSymbols(reach.SymbolInput{Graph: view.graph, Added: added, ReadFile: view.read, Symbols: provider, Resolver: view.resolver})
	if err != nil {
		return nil, fmt.Errorf("symbols: %w", err)
	}
	// A file whose added lines touch no declaration (an import-only edit) was still analyzed: it has no call
	// sites, and must not fall back to counting its importers.
	out := map[string][]reach.Symbol{}
	for path := range added {
		out[path] = nil
	}
	for _, sym := range syms {
		out[sym.File] = append(out[sym.File], sym)
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
// resolveTarget reads target as a pull request (`482`, `#482`, a URL) or else as a branch range of
// the clone at env.Dir. repo is the clone the change is read from: env.Dir's, or a cache clone.
func resolveTarget(env Env, target string) (resolved, error) {
	repo := git.Repo{Dir: env.Dir}
	inClone := repo.Check()
	ref, ok := github.ParseRef(target)
	if !ok {
		if inClone != nil {
			return resolved{repo: repo}, fmt.Errorf("%w: %s", inClone, env.Dir)
		}
		base, head, err := resolveRange(repo, target)
		return resolved{repo: repo, base: base, head: head, key: target}, err
	}
	if inClone != nil && ref.Owner == "" {
		return resolved{repo: repo}, usageError{fmt.Sprintf("PR %d: run lupa inside its clone, or pass the PR's URL", ref.Number)}
	}
	forge := env.Forge
	if forge == nil {
		forge = github.Client{}
	}
	pr, err := forge.PR(env.Dir, ref)
	if err != nil {
		return resolved{repo: repo}, err
	}
	key := fmt.Sprintf("pr-%d", pr.Ref.Number)
	remote := ""
	if inClone == nil {
		if remote, err = remoteOf(repo, pr.Ref); err != nil {
			return resolved{repo: repo}, err
		}
	}
	if remote == "" {
		if ref.Owner == "" {
			return resolved{repo: repo}, fmt.Errorf("PR %d is in %s/%s, which no remote of this clone points at", pr.Ref.Number, pr.Ref.Owner, pr.Ref.Repo)
		}
		if repo, err = cacheClone(env, pr.Ref); err != nil {
			return resolved{repo: repo}, err
		}
		remote = "origin"
	}
	base, head, err := fetchPR(repo, remote, pr)
	return resolved{repo: repo, base: base, head: head, key: key, pr: &pr, forge: forge}, err
}

// resolved is a target read as a commit range of repo; pr and forge are set for a pull request.
type resolved struct {
	repo            git.Repo
	base, head, key string
	pr              *github.PR
	forge           Forge
}

// prOf gives the map what the forge says about a pull request; nil for a branch range. When the
// open pull requests cannot be listed, the duplicate rule is skipped with a warning.
func prOf(env Env, t resolved) *mapping.PR {
	if t.pr == nil {
		return nil
	}
	pr := &mapping.PR{Number: t.pr.Ref.Number, URL: t.pr.Ref.URL(), Title: t.pr.Title, BaseRef: t.pr.BaseRef, HeadRef: t.pr.HeadRef,
		Body: t.pr.Body, LinkedIssues: t.pr.LinkedIssues}
	open, err := t.forge.OpenPRs(t.pr.Ref)
	if err != nil {
		fmt.Fprintf(env.Stderr, "lupa: open PRs not listed, duplicate check skipped: %v\n", err)
		return pr
	}
	for _, o := range open {
		pr.Open = append(pr.Open, mapping.OpenPR{Number: o.Ref.Number, Title: o.Title, BaseRef: o.BaseRef, HeadRef: o.HeadRef, Files: o.Files})
	}
	return pr
}

// remoteOf names the remote of repo whose URL points at ref's repo, or "" when none does.
func remoteOf(repo git.Repo, ref github.Ref) (string, error) {
	remotes, err := repo.Remotes()
	if err != nil {
		return "", err
	}
	for _, name := range slices.Sorted(maps.Keys(remotes)) {
		if slices.ContainsFunc(remotes[name], func(url string) bool { return github.SameRepo(url, ref) }) {
			return name, nil
		}
	}
	return "", nil
}

// cacheClone returns the cache's blobless clone of ref's repo, cloning it on first use.
func cacheClone(env Env, ref github.Ref) (git.Repo, error) {
	root := env.CacheDir
	if root == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return git.Repo{}, fmt.Errorf("no cache directory for the PR's clone: %w", err)
		}
		root = filepath.Join(dir, "lupa")
	}
	dir := filepath.Join(root, ref.Host, ref.Owner, ref.Repo)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(env.Stderr, "Cloning %s/%s into %s (first time, without file contents)\n", ref.Owner, ref.Repo, dir)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return git.Repo{}, err
		}
		// Cloning beside the target and renaming it in place means an interrupted clone is never reused,
		// and of two runs cloning at once, the second keeps the first's clone.
		tmp, err := os.MkdirTemp(filepath.Dir(dir), ".clone-*")
		if err != nil {
			return git.Repo{}, err
		}
		defer os.RemoveAll(tmp)
		if err := git.CloneBlobless(fmt.Sprintf("https://%s/%s/%s.git", ref.Host, ref.Owner, ref.Repo), filepath.Join(tmp, "repo")); err != nil {
			return git.Repo{}, err
		}
		if err := os.Rename(filepath.Join(tmp, "repo"), dir); err != nil {
			if _, statErr := os.Stat(dir); statErr != nil {
				return git.Repo{}, err
			}
		}
	}
	repo := git.Repo{Dir: dir}
	return repo, repo.Check()
}

// fetchPR fetches the PR's head and base branch into refs/lupa/pr/<n>/ from remote, without touching
// any checkout, and returns their merge-base and the head, as the PR shows them. In a blobless clone
// the head's file contents are brought in at once, since the map reads every file at head.
func fetchPR(repo git.Repo, remote string, pr github.PR) (base, head string, err error) {
	refs := fmt.Sprintf("refs/lupa/pr/%d/", pr.Ref.Number)
	if err := repo.Fetch(remote, fmt.Sprintf("+refs/pull/%d/head:%shead", pr.Ref.Number, refs), "+refs/heads/"+pr.BaseRef+":"+refs+"base"); err != nil {
		return "", "", err
	}
	if head, err = repo.Commit(refs + "head"); err != nil {
		return "", "", err
	}
	tip, err := repo.Commit(refs + "base")
	if err != nil {
		return "", "", err
	}
	if base, err = repo.MergeBase(tip, head); err != nil {
		return "", "", err
	}
	if repo.Blobless() {
		if err := repo.Prefetch(head); err != nil {
			return "", "", err
		}
	}
	return base, head, nil
}

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
	if repo.Blobless() {
		if err := repo.Prefetch(head); err != nil {
			return "", "", fmt.Errorf("prefetch %s: %w", head, err)
		}
	}
	return base, head, nil
}

// callSitesOf lists, per changed file with analyzed declarations, the call sites outside the change of the
// declarations it touches, as sorted file:line (a file with none still gets its key, so it reads as analyzed),
// and the other changed files that call them.
func callSitesOf(syms map[string][]reach.Symbol, files []mapping.File) (outside, inChange map[string][]string) {
	changed := map[string]bool{}
	for _, f := range files {
		changed[f.Path] = true
	}
	outside, inChange = map[string][]string{}, map[string][]string{}
	for file, list := range syms {
		var sites, callers []string
		for _, s := range list {
			for _, c := range s.Callers {
				site := fmt.Sprintf("%s:%d", c.File, c.Line)
				switch {
				case !changed[c.File] && !slices.Contains(sites, site):
					sites = append(sites, site)
				case changed[c.File] && c.File != file && !slices.Contains(callers, c.File):
					callers = append(callers, c.File)
				}
			}
		}
		slices.Sort(sites)
		slices.Sort(callers)
		outside[file] = sites
		if len(callers) > 0 {
			inChange[file] = callers
		}
	}
	return outside, inChange
}
