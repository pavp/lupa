# Lupa

Verified code review, locally, with the coding agent you already use.

Lupa runs your agent's CLI (Claude Code, Pi) as its review engine and checks every comment with code: a finding survives only if the code it quotes is found in the diff. It never posts and never edits.

`lupa review <pr|branch>` is the entry point, from the terminal or from your agent:

1. A deterministic map, no LLM: review now or hand back, risk by path, dependents of what changed, reading order.
2. A plan with a cost estimate, and the choice to read the change yourself first.
3. Isolated, read-only agent runs per group of files; every comment anchored to verified code; a coverage ledger of what was and was not reviewed.

`lupa map` runs the first step alone.

Status: design only. See [`docs/design.md`](docs/design.md).
