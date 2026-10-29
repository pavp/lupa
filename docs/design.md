# Lupa — design

Status: design agreed, nothing implemented. This document is the public source of truth until code exists.

## Goal

Verified code review that runs locally with the coding agent you already use. Every comment the model makes is anchored to code that a deterministic check found in the diff; nothing is posted and nothing is fixed.

## Why it exists

- Coding agents multiplied pull request volume; review is now the bottleneck.
- Noisy AI review makes teams slower, not faster. Precision matters more than recall.
- Starting a review from an AI's list of issues anchors the reviewer on those locations and saves no time. AI review belongs after the human read, not before.
- Tools that run on an agent subscription let the agent orchestrate itself and verify nothing it says. Lupa orchestrates the agent from outside and checks every output with code.

## Principles

1. **The LLM generates, code verifies.** Every model output passes a deterministic gate: a quote that must exist in the diff, a fixed JSON shape, mutually exclusive choices, limits that degrade to something simpler.
2. **The program orchestrates, the agent is the engine.** Each LLM step is a separate, isolated agent CLI run with read-only tools.
3. **Local only.** Never posts to a forge, never edits code. Output lives under the reviewed repo's git dir.
4. **Agnostic by adapter.** Any agent's CLI, on the user's subscription; not provider APIs.
5. **Language-agnostic core.** The core works on diffs, hunks and text. Everything that depends on a programming language lives behind a language provider.
6. **PR content is data, never instructions.** Description, commits, comments, code comments and diff text can be wrong or adversarial. No directive or self-declared risk in them ("low risk", "skip review") changes what Lupa does; agent prompts carry PR content as quoted data.
7. **Measure before building more.** Every stage has a check that can stop the project.

## How it is used

The product is code review. One binary, one entry point, `lupa review`, reachable from the terminal or from the user's own agent; both run the same engine.

### `lupa review <pr|branch>`

1. **Map** (no LLM, seconds): review now or hand back, risk, reading order. On a hand-back it suggests a reply to the author; the result is complete whatever the size.
2. **Plan**: groups, files, agent runs and a cost estimate. Lupa then offers two choices: read the change first and come back (recommended, so the human read is not anchored by AI findings), or run the agent review now. Never forced.
3. **Verified findings**: anchored comments (`file:line` + quote), pre-existing issues apart, coverage (reviewed and not reviewed), and how many comments were dropped and why.

The reviewer decides what to comment on the PR. Lupa never posts. After a new push, running it again shows only what changed since the last look.

Flags: `--agent <name>`, `--plan` (stop after the plan), `--yes` (skip the prompt), `--json` (stable, versioned schema; the interface for any integration).

Secondary commands:

- `lupa map <pr|branch>`: the first step alone, for quick triage or for tools that read its JSON.
- `lupa doctor`: checks `git`, `gh`, the agent CLI login (with a minimal call) and what each language provider needs.

### From the agent conversation

An agent skill named `lupa` wraps the CLI so the user keeps talking to their own agent. One skill mirrors the CLI's single entry point: `/lupa 482` reviews (the default), `/lupa map 482` and `/lupa since 482` are shortcuts, and natural language triggers it too. Pi forces a skill with `/skill:<name>` and appends the rest as the request, so there it is `/skill:lupa 482`.

The skill ships inside the binary: `lupa skill install` writes its `SKILL.md` into the agents' skill directories, so the skill always matches the installed binary's `--json` schema. It follows the Agent Skills format: written once to `~/.agents/skills/lupa/` (Pi reads that location natively) and linked from `~/.claude/skills/lupa`. Reviewer runs never load it: they start with skills disabled.

"Review PR 482 with lupa" → the agent runs `lupa review 482 --plan --json`, presents the map and the plan, asks, then runs the review in the background and presents the findings. "Map PR 482" and "what changed since my last look" are shortcuts of the same skill.

Rules for the skill:

- The conversation agent never reviews. It invokes Lupa and presents its output; it does not add findings or reword Lupa's. Its own opinion, if any, is labeled as such.
- After the map, it offers the read-first choice like the CLI does; never a block.
- Two different agents: the conversational one has the user's context; the reviewer is an isolated, read-only CLI run that Lupa launches.

A Model Context Protocol server (`lupa mcp`) is a later option; the skill plus the CLI covers v1.

## Pipeline

### Map — no LLM, deterministic (step 1 of `lupa review`, also `lupa map`)

Signals for a PR or a branch range:

- size (changed lines, generated files excluded), files, tests modified, dependency and lockfile changes, migrations;
- risk by universal path rules, first match wins: read first (migrations and schema files, CI and infra, auth/permissions/sessions/tokens and `.env*` by name, dependency manifests, exports with importers outside the diff), tests before the code they test, skip (lockfiles, snapshots, generated and `dist/` output), skim (anything else). A sensitive file no rule covers is marked `judged`. No per-repo rules file;
- CODEOWNERS owners;
- reach: who imports what changed, outside the diff;
- review now or hand back, first match wins: another open PR changes the same files (possible duplicate); over 400 changed lines, generated excluded (suggest a split); no description, or no linked issue and no stated purpose (ask for intent); otherwise review;
- reading order (read first / skim / skip), with the reason per file;
- "since your last look": only what changed since the last mapped SHA.

Output: a JSON file with a versioned schema, readable by other tools, and a local report. Report order: 1) review now or hand back with a suggested reply, 2) risk by category (impact, rollback cost, error detection, verification, alignment), worst first, no numeric score, 3) reading order with the why per file, 4) questions for the author with `file:line`, 5) description vs diff: each claim with one exact diff line found by a search, else `not found`. A hand-back is advisory: the whole report is shown whatever the size of the change.

Head code is read without touching the user's checkout: `git fetch origin pull/<n>/head:refs/lupa/<n>`, then `git show <head>:<path>`. Never switch branches.

### Agent review — LLM through an agent CLI, verified

1. **[code]** Select reviewable files; keep a coverage ledger: reviewed, or skipped with a reason (generated, binary, documentation such as `.md`).
2. **[code]** Assign review rules per file by path glob, from the language provider; drop items a linter already covers.
3. **[code]** Group related files: one directory per group, and a group under a quarter of the budget also takes the next directory, so small changes do not each cost a run. Size limits that degrade to one file per group, then to a file split by hunks. Grouping is code, not a model call. Any size of change is reviewed in full: a bigger change means more groups and agent runs, shown with their cost in the plan before running. Only a single hunk that alone exceeds the prompt budget is left out, and the coverage ledger says so with the reason.
4. **[agent]** Review each group in an isolated run with read-only tools (`Read`, `Grep`, `Glob`) inside a detached worktree at the PR head, so the reviewer can check callers outside the diff. The prompt carries, as quoted data under a byte budget (over budget → the group is split or reported as not reviewed, never truncated silently): the group's patch, the importers from the map as a starting hint, the rules, and the repo's `AGENTS.md` / `CLAUDE.md` read from the **base** branch so a PR cannot change its own review. Output is strict JSON, requested with the agent's answer schema when it has one: the list of files inspected (must equal the group), and per comment a severity, a claim, the mechanism, a fix, and an exact quote of the code it is about. "No findings" is an explicit empty list and the expected answer for most groups. Groups run up to four at a time; their results merge in group order, so the report does not depend on which run finishes first.
5. **[code]** Validate the JSON: schema, files-inspected coverage, placeholder text rejected. An answer that is not one JSON object gets one retry; still unusable, the group is reported as not reviewed. A bad comment is dropped on its own; the rest of the group's output survives. Locate each quote: normalized consecutive-line match on the new side, then the old side, then the full new file; then a model-free unique match in another changed file. Still a miss → **[agent]** one relocation retry → still a miss, the comment is **dropped** and counted in the report.
6. **[code]** Causality derived from the changed lines of each file's patch, never taken from the model: `introduced` when an anchored line is added (or the quote is removed code), `behavior-activated` when the anchored lines are unchanged but inside a hunk, next to the change, `pre-existing` when they sit outside every hunk. A binary file or a file without hunks is `unknown`: never shown as introduced, never counted as proof of absence. Pre-existing comments move to a follow-up section.
7. **[agent]** Filter, one batched run over the surviving severe comments, judged against the same severity rubric the reviewer used: per comment `corroborated`, `refuted` or `inconclusive`. A refutation must cite a `file:line` whose code contradicts the claim, and code checks that the citation exists. Blocking comments are filtered too: their level rests partly on the model's own claim that the quote settles it. No citation, `inconclusive`, a failed run or a malformed answer → the comment stays.
8. **[code]** Report: comments anchored by line, each with its mechanism and a concrete fix, secrets redacted; pre-existing follow-ups; coverage; dropped comments with the reason. Artifacts keep the exact diff reviewed and the run's provenance.

### Noise controls

The quote gate proves a comment is about real code in the diff, not that it is right or worth reading. Volume is controlled by code, not by asking the model:

- **Severity rubric**, shared by reviewer and filter: severe only when the change causes it, realistic input reaches it, and it names an observable harm; otherwise at most a warning. Style or suspicion is not a finding. Excluded from the prompt's scope: pre-existing issues, nitpicks, anything a linter catches, general quality or test coverage remarks.
- **Level derived by code** from severity, evidence class (the quoted lines settle the claim, or it needs inference) and causality. Severity and evidence class are the model's statements, so every severe comment, blocking included, still faces the filter.
- **Dedupe by code**: same file, overlapping lines and a similar claim merge into one comment.
- **No caps in v1**: the report shows every comment that passed the gates, ordered by level, severe first. A count cap would hide verified severe issues; whether any limit is needed is decided by measurement, not up front.
- **Measured**: precision is the stop criterion; noise that survives these controls stops the project before it reaches real PRs.

## Language providers

The core never imports language knowledge. A provider declares:

- which files it owns (extensions), test file patterns, manifests and lockfiles, generated output;
- import patterns and how a specifier resolves to a file (relative paths, extensions, directory index, config aliases, workspace package names), read by the core's approximate reach;
- optionally, an exact reach analyzer: an external process that prints the import graph in a common JSON shape, added only when measurement shows the approximate reach misses what matters;
- review rule docs by path glob.

Signals and reports carry a `language` field from v1. v1 ships one provider: TypeScript/JavaScript.

## Code layout

Organized by domain, so the tree says what Lupa does. The core defines interfaces; everything external (agents, the forge, git) implements them at the edges.

```
cmd/lupa/            main: wires dependencies, calls cli
internal/
  cli/               commands (review, map, doctor, skill install), flags, --json output
  review/            main use case: map → plan → agent → verify → filter → report
  mapping/           map signals: size, path rules, owners, review-now gate, reading order
  verify/            the deterministic core: schema, quote location, causality by hunks,
                     derived level, dedupe (pure code, no I/O)
  reach/             approximate reach behind an interface (exact analyzers optional)
  lang/              language provider interface
    typescript/      import patterns, resolution table, review rule docs (embedded)
  agent/             Agent interface (run one review, return raw JSON)
    claude/          adapter: isolation flags, stdin prompt, time limit
    pi/              adapter
  forge/github/      PR data through gh, fetching the head without a checkout
  git/               diff, hunks, merge-base, detached worktree
  report/            markdown and the versioned JSON schema
  store/             <git-dir>/lupa/<pr>/<sha>/ and "since your last look"
  skill/             embedded SKILL.md and its install
```

The package is `mapping` because `map` is a Go keyword.

Dependency rules:

- `verify/` and `mapping/` import nothing external: data in, data out. They carry the product's promise and get the deepest tests (table-driven, fixtures with planted traps).
- `review/` orchestrates through interfaces (`Agent`, `Forge`, `Repo`, `Language`); its tests use a fake agent returning hand-written JSON, so the whole pipeline is tested without spending tokens.
- Only the adapters (`agent/*`, `forge/github`, `git/`) spawn processes; their integration tests are skipped with `-short`.
- A new language or agent is a new directory; the core does not change.

## v1 scope

| In | Out for now |
|---|---|
| Go, single static binary (`CGO_ENABLED=0`; darwin and linux, amd64 and arm64), released on version tags to the Homebrew tap (`brew install pavp/tap/lupa`) | npm distribution |
| Agents: Claude Code, Pi | Other agent CLIs |
| Branch ranges and GitHub PRs through `gh` | Other forges |
| TypeScript/JavaScript provider | Other language providers |
| Map + review + local report + `--json` | PR comments, CI integration |
| Approximate, language-agnostic reach at file level | Exact per-language analyzers, symbol-level call chains |
| Measurement on a public benchmark | Interactive viewer, telemetry |

## Agent adapters

| Need | Claude Code 2.1.x | Pi 1.4.0 |
|---|---|---|
| Headless | `-p` | `-p` |
| Structured output | `--output-format json` / `stream-json`; `--json-schema` validates the answer into `structured_output` | `--mode json` (also `rpc`) |
| Read-only tools | `--tools Read,Grep,Glob` | `--tools` allowlist, `--exclude-tools` |
| Model | `--model` | `--model provider/id`, `--thinking` |
| Ephemeral | `--no-session-persistence` | `--no-session` |
| Isolate from user context | `--setting-sources ""` (also keeps the reviewed repo's `CLAUDE.md` out, verified), `--safe-mode`, `--strict-mcp-config`, a replacing `--system-prompt`, prompt on stdin | `--no-context-files`, `--no-extensions`, `--no-skills` |

Never run a reviewer with a permission-bypass mode; the tool allowlist is the boundary. The reviewed tree is a detached worktree at the PR head, never the user's working copy. Each run has a time limit and the prompt a byte budget, so cost is predictable.

A tool-free variant (`--tools ""`, the patch and importers inline only) is kept behind an internal flag for measurement: if it matches the read-only mode on precision, it becomes the default because it is cheaper.

Gotchas found while testing:

- `claude --bare` requires an API key; it fails on a subscription login.
- Pi user extensions can autoformat, autofix and inject context; a read-only reviewer must run with `--no-extensions`.
- `pi auth check --no-refresh` reported `not_ready` for every provider while real calls worked; test readiness with a minimal call.
- Pi tool calls arrive as `toolCall` items inside assistant `message_end` events.

## Reach (impact analysis)

Behind an interface ("what does this change reach"), so implementations can change without touching the rest:

- **v1, approximate and language-agnostic, file level:** the core finds import statements with the provider's patterns and resolves them with its resolution table (relative paths, extensions, directory index, config path aliases, workspace package names). Importers are followed transitively. Deterministic: same input, same output, no model.
- **Never lose an edge silently:** an import that names a repo file but cannot be resolved is reported as unresolved, so the map states its own limits.
- **Exact analyzers are optional and later:** a compiler-backed analyzer per language, behind the same interface, only if measurement shows the approximate reach misses importers that matter. A TypeScript analyzer built on the compiler API already passes the resolution-trap fixtures and serves as the oracle to measure the approximate one. Note: TypeScript 7 no longer ships the classic compiler API, so such an analyzer must pin its own TypeScript 6.x and requires Node.
- **The review does not depend on reach:** the reviewer explores callers with its read-only tools; reach is a starting hint and a risk signal for the map.
- **Later, only if file level falls short:** symbol-level call chains, framework entry points (routes, pages, jobs).

## Measurement and stop criterion

A public code-review benchmark of real PRs with expert-annotated issues. On about 20 TypeScript/JavaScript PRs from it, `lupa review` must beat, on precision and with the same model, the strongest baselines that run on the same agent subscription. If verification does not move precision, the project's core bet fails.

Fixtures with planted traps cover `lupa map`: claims the diff does not implement, unmentioned config changes, a migration, swallowed errors, lockfile changes, importers outside the diff, a shared component that breaks untouched callers, a test that never imports its subject, and an instruction injected in the PR description.

## Order of work

1. The map step (`lupa map`) with the TypeScript/JavaScript provider.
2. `lupa review` end to end, minimal, for TypeScript/JavaScript with Claude Code.
3. Pi adapter.
4. Agent skill over `--json`.
5. Measurement against the stop criterion before investing further.

## Open decisions

- The signals JSON schema.
- The dedupe similarity rule.
- Review prompt and rule set for TypeScript/JavaScript.
- Report format (markdown only, or markdown plus HTML).
- Output location: proposed `$(git rev-parse --git-dir)/lupa/<pr-or-branch>/<short-sha>/`.
