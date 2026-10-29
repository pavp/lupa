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

1. **Map** (no LLM, seconds): review now or hand back, risk, reading order. On a hand-back it stops here and suggests a reply to the author.
2. **Plan**: groups, files, agent runs and a cost estimate. Lupa then offers two choices: read the change first and come back (recommended, so the human read is not anchored by AI findings), or run the agent review now. Never forced.
3. **Verified findings**: anchored comments (`file:line` + quote), pre-existing issues apart, coverage (reviewed and not reviewed), and how many comments were dropped and why.

The reviewer decides what to comment on the PR. Lupa never posts. After a new push, running it again shows only what changed since the last look.

Flags: `--agent <name>`, `--plan` (stop after the plan), `--yes` (skip the prompt), `--json` (stable, versioned schema; the interface for any integration).

Secondary commands:

- `lupa map <pr|branch>`: the first step alone, for quick triage or for tools that read its JSON.
- `lupa doctor`: checks `git`, `gh`, the agent CLI login (with a minimal call) and what each language provider needs.

### From the agent conversation

An agent skill named `lupa` wraps the CLI so the user keeps talking to their own agent. One skill mirrors the CLI's single entry point: `/lupa 482` reviews (the default), `/lupa map 482` and `/lupa since 482` are shortcuts, and natural language triggers it too. How Pi invokes a skill explicitly is still to verify.

The skill ships inside the binary: `lupa skill install` writes its `SKILL.md` into the agents' skill directories, so the skill always matches the installed binary's `--json` schema.

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

Output: a JSON file with a versioned schema, readable by other tools, and a local report. Report order: 1) review now or hand back with a suggested reply, 2) risk by category (impact, rollback cost, error detection, verification, alignment), worst first, no numeric score, 3) reading order with the why per file, 4) questions for the author with `file:line`, 5) description vs diff: each claim with one exact diff line found by a search, else `not found`. A hand-back stops after section 2.

Head code is read without touching the user's checkout: `git fetch origin pull/<n>/head:refs/lupa/<n>`, then `git show <head>:<path>`. Never switch branches.

### Agent review — LLM through an agent CLI, verified

1. **[code]** Select reviewable files; keep a coverage ledger: reviewed, or skipped with a reason.
2. **[code]** Assign review rules per file by path glob, from the language provider; drop items a linter already covers.
3. **[code]** Group related files, with size limits that degrade to one file per group. Grouping is code, not a model call.
4. **[agent]** Review each group in an isolated run, read-only tools, no user context. The repo's `AGENTS.md` / `CLAUDE.md`, read from the **base** branch so a PR cannot change its own review, are passed as quoted team conventions, size-capped. Output is JSON: comment, severity, and an exact quote of the code it is about.
5. **[code]** Validate the JSON and locate each quote: normalized consecutive-line match on the new side, then the old side, then the full new file; then a model-free unique match in another changed file. Still a miss → **[agent]** one relocation retry → still a miss, the comment is **dropped** and counted in the report.
6. **[code]** Causality by changed hunks (`git diff --unified=0`): `introduced`, `behavior-activated` (the change makes a pre-existing defect reachable) or `pre-existing`. Pre-existing comments move to a follow-up section.
7. **[agent]** Filter: may remove a comment only by citing a `file:line` whose code contradicts it. A comment whose cited lines alone settle the claim is checked by code instead of a model. No citation, a failed run or a malformed answer → the comment stays.
8. **[code]** Report: comments anchored by line, each with its mechanism and a concrete fix, secrets redacted; pre-existing follow-ups; coverage; dropped comments with the reason. Artifacts keep the exact diff reviewed and the run's provenance.

## Language providers

The core never imports language knowledge. A provider declares:

- which files it owns (extensions), test file patterns, manifests and lockfiles, generated output;
- a reach analyzer: an external process that prints the import graph in a common JSON shape, so providers can be written in any language;
- review rule docs by path glob.

Signals and reports carry a `language` field from v1. v1 ships one provider: TypeScript/JavaScript.

## v1 scope

| In | Out for now |
|---|---|
| Go, single binary, Homebrew tap (`brew install pavp/tap/lupa`) | npm distribution |
| Agents: Claude Code, Pi | Other agent CLIs |
| Branch ranges and GitHub PRs through `gh` | Other forges |
| TypeScript/JavaScript provider | Other language providers |
| Map + review + local report + `--json` | PR comments, CI integration |
| Reach at file level (import graph) | Symbol-level call chains |
| Measurement on a public benchmark | Interactive viewer, telemetry |

## Agent adapters

| Need | Claude Code 2.1.x | Pi 1.4.0 |
|---|---|---|
| Headless | `-p` | `-p` |
| Structured output | `--output-format json` / `stream-json` | `--mode json` (also `rpc`) |
| Read-only tools | `--tools Read,Grep,Glob` | `--tools` allowlist, `--exclude-tools` |
| Model | `--model` | `--model provider/id`, `--thinking` |
| Ephemeral | default with `-p` | `--no-session` |
| Isolate from user context | `--setting-sources ""`, `--strict-mcp-config` | `--no-context-files`, `--no-extensions`, `--no-skills` |

Never run a reviewer with a permission-bypass mode; the tool allowlist is the boundary. The reviewed tree is a detached worktree at the PR head, never the user's working copy.

Gotchas found while testing:

- `claude --bare` requires an API key; it fails on a subscription login.
- Pi user extensions can autoformat, autofix and inject context; a read-only reviewer must run with `--no-extensions`.
- `pi auth check --no-refresh` reported `not_ready` for every provider while real calls worked; test readiness with a minimal call.
- Pi tool calls arrive as `toolCall` items inside assistant `message_end` events.

## Reach (impact analysis)

Behind an interface ("what does this change reach"), so implementations can change without touching the rest:

- **v1, file level:** importers of each changed file, transitively up to entry points. Exact for TypeScript/JavaScript only when resolved like the compiler does (path aliases, `extends`, barrels, re-exports, `import type`, package `exports`, workspaces).
- **Later, only if file level falls short:** symbol-level call chains, framework entry points (routes, pages, jobs).

Open: Go cannot run the TypeScript compiler in-process. Recommended: a small Node script that uses the project's own TypeScript compiler API, validated on fixtures with planted resolution traps before it is adopted.

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

- How the TypeScript/JavaScript provider computes reach (Node subprocess recommended).
- The signals JSON schema.
- Review prompt and rule set for TypeScript/JavaScript.
- Report format (markdown only, or markdown plus HTML).
- Output location: proposed `$(git rev-parse --git-dir)/lupa/<pr-or-branch>/<short-sha>/`.
