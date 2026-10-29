# AGENTS.md

Guidance for AI agents working in **lupa**: a local CLI, written in Go, that runs coding-agent CLIs (Claude Code, Pi) as a code review engine and verifies every model output with code.

## Read first

`docs/design.md` holds the goal, principles, modes, v1 scope, verified agent adapter flags, the stop criterion and the open decisions. Read it before proposing or writing anything; it is the source of truth until code exists.

This repo will be open source. Never name other tools, repos or projects in tracked files; research, comparisons and private context live outside the repo.

## Non-negotiables

- The LLM generates, code verifies: no model output reaches the report without passing a deterministic check.
- Never post to a forge and never edit the reviewed code. Output lives under the reviewed repo's git dir.
- Agent runs are isolated and read-only, with the user's context, extensions and skills turned off.
- Commits use Conventional Commits. No AI attribution / `Co-Authored-By` lines.
- Branch off `main`; never commit directly to `main` after the initial commit. Open a PR.
