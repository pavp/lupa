// Package cli parses lupa's command line and writes its output.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pavp/lupa/internal/git"
	"github.com/pavp/lupa/internal/lang/typescript"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/owners"
	"github.com/pavp/lupa/internal/reach"
	"github.com/pavp/lupa/internal/store"
)

const usage = "usage: lupa map <base>..<head> | <branch> [--json]"

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
	default:
		return usageError{fmt.Sprintf("unknown command %q", args[0])}
	}
}

func runMap(args []string, env Env) error {
	var target string
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return usageError{fmt.Sprintf("unknown flag %q", a)}
		case target != "":
			return usageError{fmt.Sprintf("unexpected argument %q", a)}
		default:
			target = a
		}
	}
	if target == "" {
		return usageError{"missing target"}
	}

	repo := git.Repo{Dir: env.Dir}
	if err := repo.Check(); err != nil {
		return fmt.Errorf("%w: %s", err, env.Dir)
	}
	base, head, err := resolveRange(repo, target)
	if err != nil {
		return err
	}
	changes, err := repo.NumStat(base, head)
	if err != nil {
		return err
	}
	files := make([]mapping.File, len(changes))
	for i, c := range changes {
		files[i] = mapping.File{Path: c.Path, Additions: c.Additions, Deletions: c.Deletions, Binary: c.Binary}
	}
	r, err := reachOf(repo, head, files)
	if err != nil {
		return err
	}
	signals := mapping.Build(mapping.Input{Base: base, Head: head, Files: files, Reach: r, Owners: ownersOf(repo, base, files)})
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
	if _, err := st.Save(target, head, append(saved, '\n'), nil); err != nil {
		return fmt.Errorf("saving the map: %w", err)
	}

	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(signals)
	}
	_, err = fmt.Fprintf(env.Stdout, "%d files, +%d -%d (%d changed lines)\n",
		signals.Size.Files, signals.Size.Additions, signals.Size.Deletions, signals.Size.Changed)
	return err
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
	out := mapping.Reach{Importers: map[string][]string{}}
	for _, f := range changed {
		for _, imp := range g.Importers(f.Path) {
			if !inChange[imp] {
				out.Importers[f.Path] = append(out.Importers[f.Path], imp)
			}
		}
	}
	for _, u := range g.Unresolved {
		if inChange[u.From] {
			out.Unresolved = append(out.Unresolved, mapping.Unresolved{From: u.From, Specifier: u.Specifier})
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
