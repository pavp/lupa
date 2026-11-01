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

Needs `git`; Claude Code installed and logged in (reviews run on your subscription); `gh` logged in for PR targets; Pi only for `--agent pi`. `lupa doctor` checks them, with one minimal Claude Code call.

```sh
brew install pavp/tap/lupa  # macOS and Linux; or: go install github.com/pavp/lupa/cmd/lupa@latest
lupa doctor                 # git, gh, Claude Code login, Pi

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

From your agent: `lupa skill install` writes the `lupa` skill to `~/.agents/skills/lupa/` (Pi reads it there) and links `~/.claude/skills/lupa` to it for Claude Code; then ask `/lupa 482`, `/lupa map feature` or "what changed since my last look at 482". The agent runs lupa, shows the map and the plan, asks before reviewing, and presents lupa's report without reviewing the code itself. Run it again after upgrading lupa; it never replaces a `lupa` skill it did not write.

`lupa review` runs Claude Code (or Pi with `--agent pi`) isolated and read-only in a detached worktree at the head, on your subscription, one run per group; a change that fits the prompt budget is one group. `--model` picks the model and `--effort` Claude Code's reasoning effort. `--budget <bytes>` sets the patch bytes one group may carry (default 60000), `--parallel <n>` how many reviewer runs go at once (default 4) and `--timeout <duration>` the limit of each run, such as `90s` or `15m` (default 10m); each falls back to `LUPA_BUDGET`, `LUPA_PARALLEL` or `LUPA_TIMEOUT` when the flag is not given. `--investigate` adds a read-only investigator per finding (Pi always has one); `--orchestrate` instead runs one Claude Code session that reviews every group and validates each comment through read-only subagents. Pi runs with none of your extensions: name the ones it needs, such as the extension that provides your model, with `--pi-extensions`. Every comment must quote code that is found in the diff, or it is dropped; causality comes from the changed hunks, never from the model, and code derives the level from it and the model's severity; a finding on added lines that were all removed elsewhere in the change (moved code, reindented or not) is a follow-up, like pre-existing code, when nothing else changed around it, and keeps its level otherwise, since moving code into edited surroundings can break it; findings on overlapping lines with similar claims are merged, and so are findings anywhere in the change that name the same code symbol, have the same harm and similar claims (one defect seen from several files), keeping every location; a warning the reviewer says has an observable harm (wrong data, a failure, a security hole, a broken caller) gets a read-only investigator and joins the main list only when code finds the evidence it cites, otherwise it stays a note, the same rule with `--investigate` and with `--orchestrate`, whose validator decides it; a contract pass, one more run per group outside `--orchestrate`, checks only the unchanged code that reads what the change writes or uses the constants it uses, and its findings join the main list only when an investigator confirms them; a contract break on lines a reviewer finding already covers is the same defect seen from its consumer, so it is merged into that finding, the higher level kept and both mechanisms shown; with investigation on, every finding but a pre-existing or purely moved follow-up gets its own read-only investigator and stays only when code finds the evidence it cites at its line; it is refuted only with found code other than the flagged lines and the step of the mechanism it makes impossible, and anything unproven is listed as unconfirmed with the reason. For a PR, the description's claims in prose are checked in one more run: each is `supported` by a changed line that code finds in the diff, is only mentioned in a comment or docs, has no supporting line, or cannot be judged from a diff. The report reads in the order a reviewer needs it: whether to review now, the description claims, the findings to check (blocking and important) with their mechanism, fix and evidence, a reading order that starts with the files those findings are in, the minor findings one line each, the coverage, what was unconfirmed, refuted or dropped and why, and the run's notes; secrets are redacted. It is saved next to the map. The change goes to the model unredacted, as an accepted risk: the agent runs in your environment, which already holds the change, and redacting it would break the check that every quote is found in the diff and hide the credential the review should report; what lupa saves and prints is redacted. Where a policy forbids sending credentials to a model, `--redact-prompt` replaces them in everything lupa sends and in the files of its own review worktree, removes the links there that lead out of it, and drops quotes of the redacted lines; the agent's read-only tools can still open files outside that worktree, so it reduces the exposure without guaranteeing it. A PR's head and base are fetched into `refs/lupa/pr/<n>/` from the remote that points at its repo, the base by its commit when its branch was deleted (a stacked PR whose parent merged); the checkout is never touched. A PR URL of a repo you have not cloned is read from a clone without file contents that Lupa keeps in your cache directory (`lupa/<host>/<owner>/<repo>`); its reports are saved there. Exit code: 0 on success, 1 when no group could be reviewed (the report is still saved), 2 on a usage or input error.

The text report gives: review now or hand back, risk by category (no score), the reading order with the reason for each file, and questions for the author with `file:line`, and for a pull request, each claim of its description that cites code with the diff line that has it, or `not found`; claims in prose are left to `lupa review`. Added lines that hold a credential are listed by `file:line` and kind, never by value; one with a provider's structure (a private key, `AKIA…`, `ghp_…`, `sk-…`, `sk_live_…`, `xox…`) hands the change back to rotate it, wherever it is, fixtures included, and `lupa review` reports it as a blocking finding raised by code, not the model; a value next to a credential-like name (`password = …`, `Bearer …`) is an important "possible secret, verify". Every map is saved under `.git/lupa/<target>/<short-sha>/`; the next run shows only what changed since then. TypeScript/JavaScript reach needs no `node_modules` and no Node.

## Release

Push a `vX.Y.Z` tag: the release workflow runs GoReleaser, publishes the archives on GitHub Releases and updates the formula in `pavp/homebrew-tap`. It needs a `LUPA_HOMEBREW_TAP_TOKEN` secret with write access to that tap. Validate locally with `goreleaser release --snapshot --clean --skip=publish`. Versions follow Semantic Versioning; while the version is `0.y.z`, a breaking change bumps the minor.
