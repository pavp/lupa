# Lupa

Verified code review, locally, with the coding agent you already use.

Lupa runs your agent's CLI (Claude Code, Pi) as its review engine and checks every comment with code: a finding survives only if the code it quotes is found in the diff. It never posts and never edits.

`lupa review <pr|branch>` is the entry point, from the terminal or from your agent:

1. A deterministic map, no LLM: review now or hand back, risk by path, dependents of what changed, reading order.
2. A plan with a cost estimate, and the choice to read the change yourself first.
3. Isolated, read-only agent runs per group of files; every comment anchored to verified code; a coverage ledger of what was and was not reviewed.

`lupa map` runs the first step alone.

## Status

`lupa map` and `lupa review` work on local branch ranges and on GitHub PRs, with Claude Code; Pi is next. See [`docs/design.md`](docs/design.md).

```sh
go install github.com/pavp/lupa/cmd/lupa@latest

lupa map feature            # the branch against the default branch, as a PR would show it
lupa map main..feature      # an explicit range
lupa map feature --json     # the signals, schema in docs/signals-schema.md
lupa map 482                # a GitHub PR of this clone's repo, also #482 or its URL (needs gh)

lupa review feature --plan  # groups, agent runs and a token estimate; nothing runs
lupa review feature         # shows the plan, asks, then reviews with Claude Code
lupa review 482             # the same for a PR
lupa review https://github.com/org/repo/pull/482   # from anywhere, even without a clone
lupa review feature --yes --json
lupa review feature --agent pi --model <provider/id> --pi-extensions <path>   # through Pi, e.g. a provider's extension
```

`lupa review` runs Claude Code (or Pi with `--agent pi`) isolated and read-only in a detached worktree at the head, on your subscription, one run per group; a change that fits the prompt budget is one group. `--model` picks the model and `--effort` Claude Code's reasoning effort. `--investigate` adds a read-only investigator per finding (Pi always has one); `--orchestrate` instead runs one Claude Code session that reviews every group and validates each comment through read-only subagents. Pi runs with none of your extensions: name the ones it needs, such as the extension that provides your model, with `--pi-extensions`. Every comment must quote code that is found in the diff, or it is dropped; causality comes from the changed hunks, never from the model, and code derives the level from it and the model's severity; a warning the reviewer says has an observable harm (wrong data, a failure, a security hole, a broken caller) gets a read-only investigator and joins the main list only when code finds the evidence it cites, otherwise it stays a note; with investigation on, every finding but a pre-existing follow-up gets its own read-only investigator and stays only when code finds the evidence it cites at its line; it is refuted only with found code other than the flagged lines and the step of the mechanism it makes impossible, and anything unproven is listed as unconfirmed with the reason. For a PR, the description's claims in prose are checked in one more run: each is `supported` by a changed line that code finds in the diff, is only mentioned in a comment or docs, has no supporting line, or cannot be judged from a diff. The report lists every finding with its mechanism, fix and evidence, the description claims, the coverage, what was unconfirmed, refuted or dropped and why, and the run's notes; secrets are redacted. It is saved next to the map. A PR's head and base are fetched into `refs/lupa/pr/<n>/` from the remote that points at its repo; the checkout is never touched. A PR URL of a repo you have not cloned is read from a clone without file contents that Lupa keeps in your cache directory (`lupa/<host>/<owner>/<repo>`); its reports are saved there. Exit code: 0 on success, 1 when no group could be reviewed (the report is still saved), 2 on a usage or input error.

The text report gives: review now or hand back, risk by category (no score), the reading order with the reason for each file, and questions for the author with `file:line`, and for a pull request, each claim of its description that cites code with the diff line that has it, or `not found`; claims in prose are left to `lupa review`. Every map is saved under `.git/lupa/<target>/<short-sha>/`; the next run shows only what changed since then. TypeScript/JavaScript reach needs no `node_modules` and no Node.
