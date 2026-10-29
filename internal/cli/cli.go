// Package cli parses lupa's command line and writes its output.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pavp/lupa/internal/git"
	"github.com/pavp/lupa/internal/lang/typescript"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/owners"
	"github.com/pavp/lupa/internal/reach"
	"github.com/pavp/lupa/internal/report"
	"github.com/pavp/lupa/internal/review"
	"github.com/pavp/lupa/internal/store"
)

const usage = "usage: lupa map <base>..<head> | <branch> [--json]\n       lupa review <base>..<head> | <branch> [--plan] [--json]"

// Env is what a command reads and writes: the working directory and the output streams.
type Env struct {
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
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

// parseArgs reads one target and the given boolean flags; any other flag or argument is a usage error.
func parseArgs(args []string, flags map[string]*bool) (target string, err error) {
	for _, a := range args {
		switch {
		case flags[a] != nil:
			*flags[a] = true
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
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON})
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

func runReview(args []string, env Env) error {
	asJSON, planOnly := false, false
	target, err := parseArgs(args, map[string]*bool{"--json": &asJSON, "--plan": &planOnly})
	if err != nil {
		return err
	}
	if !planOnly {
		return errors.New("the agent review is not available yet; use --plan")
	}
	repo, signals, err := mapChange(env, target)
	if err != nil {
		return err
	}
	patches, err := repo.Patches(signals.Base, signals.Head)
	if err != nil {
		return err
	}
	plan := review.Plan(signals.Files, patches, review.Options{})
	if asJSON {
		out, err := json.MarshalIndent(planJSON{Schema: "lupa.plan/v0", Base: signals.Base, Head: signals.Head, Result: plan}, "", "  ")
		if err != nil {
			return err
		}
		_, err = env.Stdout.Write(append(out, '\n'))
		return err
	}
	_, err = io.WriteString(env.Stdout, planText(plan, target))
	return err
}

func planText(p review.Result, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %d groups, %d agent runs, about %d tokens (estimate)\n", len(p.Groups), p.Estimate.Runs, p.Estimate.Tokens)
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
	fmt.Fprintf(&b, "\nRead the change first (recommended): lupa map %s\nThen run the agent review: lupa review %s --yes\n", target, target)
	return b.String()
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
