# Lupa

Verified code review, locally, with the coding agent you already use.

Lupa runs your agent's CLI (Claude Code, Pi) as its review engine and checks every comment with code: a finding survives only if the code it quotes is found in the diff. It never posts and never edits.

`lupa review <pr|branch>` is the entry point, from the terminal or from your agent:

1. A deterministic map, no LLM: review now or hand back, risk by path, dependents of what changed, reading order.
2. A plan with a cost estimate, and the choice to read the change yourself first.
3. Isolated, read-only agent runs per group of files; every comment anchored to verified code; a coverage ledger of what was and was not reviewed.

`lupa map` runs the first step alone.

## Status

`lupa map` works on local branch ranges; `lupa review` and GitHub PRs are next. See [`docs/design.md`](docs/design.md).

```sh
go install github.com/pavp/lupa/cmd/lupa@latest

lupa map feature            # the branch against the default branch, as a PR would show it
lupa map main..feature      # an explicit range
lupa map feature --json     # the signals, schema in docs/signals-schema.md
```

The text report gives: review now or hand back, risk by category (no score), the reading order with the reason for each file, and questions for the author with `file:line`. Every map is saved under `.git/lupa/<target>/<short-sha>/`; the next run shows only what changed since then. TypeScript/JavaScript reach needs no `node_modules` and no Node.
