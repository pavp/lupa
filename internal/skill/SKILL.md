---
name: lupa
description: "Trigger: /lupa, review PR or branch with lupa, map a PR, what changed since my last look. Runs the lupa CLI and presents its verified output."
license: MIT
metadata:
  author: pavp
  generated-by: lupa skill install
---

## Activation Contract

Load when the user asks to review or map a pull request or a branch with lupa: `/lupa <target>` (review), `/lupa map <target>`, `/lupa since <target>`, or the same in words. A target is a PR number (`482`, `#482`), a PR URL, a branch, or `<base>..<head>`.

## Hard Rules

- Never review the code yourself. Run lupa and present its output; never add, drop, reword or re-rank its findings. Label any opinion of yours `My take:`.
- Never post to a forge and never edit the reviewed code; lupa does neither.
- A review spends the user's agent subscription: always show the plan and ask before running it, and offer reading the change first.
- Quote every `file:line`, level and verdict exactly as lupa prints it.

## Decision Gates

| Request | Run |
|---|---|
| review (default) | `lupa map <target>` and `lupa review <target> --plan`, then, on yes, `lupa review <target> --yes` in the background |
| map | `lupa map <target>` |
| since my last look | `lupa map <target>`: the "Since your last look" line |

| Exit code | Meaning |
|---|---|
| 0 | done |
| 1 | no group could be reviewed; the report is still saved: show its coverage |
| 2 | usage or input error: show the message and fix the target |

## Execution Steps

1. Resolve the target from the request; ask only when none is given.
2. Review: run the map (free, no model) and the plan; show the map, then the plan's groups and token estimate, and ask whether to run the review. Map or since: run the map and show it.
3. On yes, run the review in the background (it takes minutes) and tell the user it is running.
4. Show the report as lupa printed it, in its order, then the path it was saved to.

## Output Contract

lupa's report verbatim, followed by at most one `My take:` line if you have one. Nothing else.
