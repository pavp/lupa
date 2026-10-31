# Signals schema — `lupa.signals/v0`

`lupa map --json` prints this object; every map is also saved as `signals.json` (and the report as `map.md`) under `<git-dir>/lupa/<target>/<short-sha>/`. Check `schema` before reading anything else; a breaking change gets a new version.

| Field | Type | Meaning |
|---|---|---|
| `schema` | string | `lupa.signals/v0` |
| `base`, `head` | string | Full commit hashes. `base` is the merge-base of the target, the diff a PR would show. |
| `files[]` | array | One entry per changed file, in `git diff` order. |
| `files[].path` | string | Repo-relative path; the new path of a renamed file. |
| `files[].old_path` | string, optional | The path before a rename, at git's default similarity; absent otherwise. |
| `files[].additions`, `files[].deletions` | number | Line counts; `0` for binary files. |
| `files[].binary` | bool, optional | `true` for binary files. |
| `files[].first_line` | number, optional | First changed line on the head side; absent for a deleted file. |
| `files[].class` | string | `read-first`, `test`, `skim` or `skip`, from the universal path rules (first match wins). |
| `files[].risk` | string | `one-way-door`, `impact`, `verification` or `none`. There is no numeric score. |
| `files[].reason` | string | Why the rule applied, e.g. `migration or schema: hard to roll back`, `imported by 3 files outside this change`. |
| `files[].importers` | string[], optional | Files outside the change that reach this one through imports, directly or transitively. |
| `files[].direct_importers` | string[], optional | The `importers` that import this file themselves; the report names these and counts the rest. |
| `files[].call_sites` | string[], optional | TS/JS: `file:line` call sites outside the change of the declarations the change touches in this file. When the file's declarations were analyzed, these, not the `importers`, decide its impact: a file whose changed declarations have no caller outside the change is not marked as impact. |
| `files[].owners` | string[], optional | Owners from the base branch's CODEOWNERS; absent when none. |
| `size` | object | `files`, `additions`, `deletions`, `changed` (additions + deletions, without lines that only change whitespace, except in `.py`, `.yml`, `.yaml` and Makefiles, where indentation changes what runs) over authored files; `generated_files` counts the skipped generated ones, which add no lines. |
| `pr` | object or null | For a pull request: `number`, `url`, `title`; null for a branch range. |
| `review_now` | object | `decision`: `review` or `hand-back`; `reason`; `reply`, the suggested reply to the author, on a hand-back only. First match wins: another open PR changes at least half of the same files, generated excluded (possible duplicate); over 400 changed lines, generated excluded (suggest a split); no description, or fewer than 10 words of description and no linked issue (ask for intent). The PR rules apply to pull requests only; the decision is advisory and every other field is still filled. |
| `since_last_look` | object or null | `null` on the first map of a target; else `since` (the last mapped head) and `files` changed since then. |
| `reading_order[]` | array | `path`, `section` (`read-first`, `skim`, `skip`) and `why`. A test sits right before the file it tests when it imports it. |
| `tests_not_importing_subject[]` | array | `test`, `subject` and `line` of a changed test named after a changed file that does not import it, so it may not exercise it. Tests are paired with their subject only when they import it. |
| `claims[]` | array or null | null for a branch range. One entry per sentence or list item of the PR description, without HTML comments, headings, checkboxes, fenced code and lines that open with an HTML tag: `text`, and `cites`, the code spans without spaces, file paths and changed file names it names, in order; null when it names none, so it is prose and not checked. Per cite: `text`, `found`, and when found `path` plus, unless the cite names a changed file, `side` (`+` added, `-` removed), `line` on that side and `code`, the first changed line, by path, holding the cite as a whole token. |
| `unresolved[]` | array | `from`, `specifier` and `line` of imports in changed files that name a repo file lupa could not find. Reach states its limits instead of guessing. |
| `ignored_imports[]` | array, optional | `from`, `specifier` and `line` of imports in changed files that name a path the repo's `.gitignore` covers: most often a generated module, sometimes a file left out by mistake. Shown in the risk section and never asked about; omitted when empty. |
| `not_analyzed[]` | array, optional | Extensions (e.g. `.go`) of changed, non-generated code files whose imports and call sites reach does not follow; the report says so in the risk section. Omitted when every changed code file was analyzed. |
| `silenced_errors[]` | array | `file`, `line` and `code` of added lines, outside tests and generated files, that swallow an error: an empty `catch`, a `.catch` returning nothing, `except: pass`, `_ = err`, and `\|\| true` in shell, YAML, Makefile and Dockerfile. Each gets a risk line and, for the first three, a question. |
| `removed_exports[]` | array, optional | `file`, `name` and `users` (importer `file:line`) of exported TS/JS declarations the change removes or renames while files outside it still import them by name. The file becomes a read-first one-way door. |
| `files[].history` | object, optional | From the base's git log over the year before the base commit (anchored there, never at the current time); absent for a file new in the change. `fix_commits`: commits whose subject says fix, bug, fault, regression or revert (by message, so approximate); `commits_90d`; `authors` (by email); `top_author_share` (0–1); `author_first_change`: the head commit's author has no commit on the file in that year. Past fixes break ties inside a reading-order section, never across; the report shows fix commits and a first change as evidence, never a score. |

Reach is approximate and covers TS/JS only today: imports are found and resolved by code (relative paths, extensions, directory index, tsconfig `paths`/`baseUrl` with `extends`, workspace packages), never by a model. For changed code in other languages, `not_analyzed` says so instead of leaving the impact silently empty.
