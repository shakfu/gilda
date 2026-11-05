# Suggestions

Improvements to gilda as a coding agent, from a review of `README.md`, `TODO.md`, `docs/dev/eval-baseline.md`, `docs/dev/tools.md`, `prompt/prompt.go` and the tool definitions. Written 2026-11-05. Nothing here was measured; each item should be checked with `scripts/eval` before it is kept.

The infrastructure (permissions, sessions, caching, retries, elision) is careful work. The gaps are in how the model is guided, and in evidence.

## High impact, low cost

1. **The system prompt is one sentence** (`prompt/prompt.go`, `base`). The local baseline shows the cost: gemma-4 built or tested in 3/24 runs, Qwen3-4B in 0/24. Add short working rules:
   - read before editing; match the file's style;
   - after changing code, run the project's build or tests; never claim success unverified;
   - do not revert or overwrite changes you did not make; check `git status`/`git diff` first;
   - make independent tool calls in the same turn;
   - prefer a dedicated tool over `bash` (`tools.md` shows OpenAI models skipping `read`);
   - end with a short summary: what changed, what was checked, what is left.

   The prompt is fixed per session, so this does not hurt caching.

2. **`edit` failures give the model nothing to recover with** (`tool/edit.go`). "old_string not found" should show the nearest match with line numbers, and retry with whitespace and indentation normalised, saying so when it did. This is the most common edit failure of weaker models.

3. **The environment states only the branch.** Add the project type (`go.mod`, `package.json`, `Makefile`, ...) and its likely build and test commands. A dirty-tree summary would also help but conflicts with "no `git` process at startup": in an untrusted checkout, `git status` can run `core.fsmonitor` or clean filters from the repository's config.

## Measurement

4. **No cloud eval baseline** (TODO step 2). It blocks E3, E6 and the `task` decisions; do it first.
5. **The 8 eval tasks are small.** They separate weak models from strong ones, not good agent behaviour from bad. Add: a failing test whose cause is in another package; a refactor that must keep a suite green; a task long enough to trigger elision; an ambiguous request where the right move is to ask; a repository with uncommitted user changes that must survive. Score tokens, cost and wall time alongside pass rate.
6. **Record tool-use mix** (`scripts/tally`) in every eval run, to track routing such as `read` versus `cat` through `bash`.

## Features, by expected value

7. **Undo and checkpoints.** Snapshot files before each `write`, `edit` or `apply_patch` (the previews already hold the old content) and add `/undo` for the last turn. Makes the `auto` default much safer; cheap.
8. **`/compact`.** Elision covers tool output; long text-heavy sessions still hit 95%. Matters most for local models with 32k windows.
9. **A plan/todo tool** for multi-step tasks, shown in the REPL. Measure whether it lifts pass rates on the larger tasks from item 5.
10. **Head and tail truncation of `bash` output.** Unchecked how it truncates today; if it keeps only the head, test failures at the end are lost.
11. **Diagnostics after edit.** Optionally run a configured formatter or linter (`gofmt -l`, `go vet` on the package) and append its findings to the tool result.
12. **Sandbox** (already High in TODO). Decide on sanduk before MCP or `[[tools.command]]`; `auto` running `bash` unconfined is the largest risk.

## Smaller

- `grep`/`glob`: keep deferred until tally data supports them.
- Show a diff of a changed `AGENTS.md` at the trust question.
- E2: when resuming with a different tool set, turn calls to tools no longer defined into text.
- `llm` coverage at 63%: retry and stream-resend paths are where silent bugs hide.

## Status

Items 1-3 done 2026-11-05 (see `CHANGELOG.md`), unmeasured. Items 5 and 6 done the same day; item 4 is ready to run (`scripts/baseline.sh`, with the rules A/B through `[prompt] rules`) but needs provider keys, which the session lacked. Item 3 has no dirty-tree summary, for the reason given; the working rules tell the model to run `git status` instead.

## Order

1 → 4 → 2 → 7 → 5 → 3, then by measured need.
