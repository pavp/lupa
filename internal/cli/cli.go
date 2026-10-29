// Package cli parses lupa's command line and writes its output.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pavp/lupa/internal/git"
	"github.com/pavp/lupa/internal/mapping"
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
	signals := mapping.Build(base, head)

	if asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(signals)
	}
	_, err = fmt.Fprintf(env.Stdout, "base %s\nhead %s\n", signals.Base, signals.Head)
	return err
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
