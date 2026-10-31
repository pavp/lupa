# Lupa — product

This document says who Lupa is for, which job it does, and how we know it works. It takes precedence over the goal, rationale and principles in `design.md`, which describe the first version.

## Who it is for

A developer reviewing **someone else's** pull request, who wants to review it **faster** and **without missing anything** that matters.

Not the author checking their own change before opening it, and not a bot that posts on the forge.

## The job

A reviewer opening a PR has to answer, in this order:

1. **Should I review this now, or hand it back?** Duplicate of another open PR, too large to review well, no stated intent.
2. **What does it actually do, and does that match what it says?** Each claim in the description against the diff.
3. **Where do I start, and what can I skim?** Risk by category, reading order with the reason per file.
4. **What else does it touch outside the diff?** Importers, callers and callees of what changed.
5. **What could be wrong here?** Places worth checking, each anchored to a line, with the mechanism and why it matters.

Lupa answers all five before or alongside the human read. Steps 1–4 are deterministic and need no model. Step 5 uses the coding agent the reviewer already has.

## What makes it useful

- **A few real, risky defects over many minor ones.** The main list holds defects the change introduces, that realistic input reaches, with an observable harm (wrong data, a failure, a security hole, a broken caller). Style, naming, logging, docs, small performance remarks and other minor points never compete with them: they go to a separate, collapsed list below the main one. Missing a risky defect costs far more than an extra item, so recall is measured on risky defects only.
- **Every item is quick to verify.** A candidate cites the exact changed line, states the mechanism and the harm, and says whether the change introduced it. Code checks that the cited line exists in the diff and derives causality from the hunks; an unanchored claim never reaches the reviewer.
- **A model may discard, never silently.** Lupa uses a model to drop plausible candidates that are wrong, so the reviewer does not spend time on them. A discard names the step of the candidate's mechanism that its evidence makes impossible; a mitigation (a retry, a scheduled job, a later cleanup) narrows a defect and is not a discard. Every discarded candidate stays visible with its reason, one step away from the main list. A discard step ships only if it lowers wrong items without lowering defects surfaced.
- **Local and agent-agnostic.** Runs on the reviewer's machine with the agent CLI and subscription they already use, including cheaper models. Never posts, never edits the code under review.
- **PR content is data.** Nothing written in the PR changes what Lupa does.

## How we measure it

- **Risky defects surfaced:** distinct real, risky defects in the main list, on a fixed set of PRs labelled by us (benchmark goldens alone are incomplete). Minor findings do not count.
- **Cost to check:** items in the main list per PR, and how many are wrong or minor; both cost the reviewer time.
- **Cost to run:** subscription dollars per PR.
- **Time to orient:** how long until the reviewer can answer questions 1–4 (to be defined).

A change ships when it improves risky defects surfaced at equal or lower cost, or keeps them at lower cost. The rule is set before the run.

## Where we are (2026-10-31)

- Steps 1–4 exist (`lupa map`).
- Step 5, pipeline version: clean (0 wrong items on 10 labelled PRs) but misses defects (20 of 28).
- Step 5, single-session version with validating subagents: about 13 times the cost of the pipeline on 3 PRs, with fewer benchmark defects found.
- The refuting step rarely acted and sometimes removed real defects.

## Next

1. Find where the pipeline loses each missed defect: never proposed, lost at anchoring, refuted, or ranked too low.
2. Fix the dominant cause, measured with the rule above.
3. Present findings as places to check, ordered for the reviewer, next to the map.

## Out of scope for now

Posting to a forge, fixing code, CI integration, reviewing one's own change before opening it. Work targets Claude Code first; other agents keep working as they do today but get no new effort until Lupa works well on Claude Code.
