# TODO

Items tagged F1-F8 and m1-m10 come from `REVIEW.md` (commit `1156cd0`, review of 2026-09-27). The review's own severity follows each tag. Items tagged E1-E7 come from `REVIEW_FEATURES.md` (commit `091c597`, review of 2026-10-04); E7 is the sandbox item below.

## Next, in order

Set 2026-10-04. Each step needs at least one provider key.

1. **Live checks.** Images in a Responses `function_call_output` and through OpenRouter (E5); whether Fable 5.1 and Opus 5.5 accept elided tool results (E1); resuming with a different tool set (E2).
2. **Eval baseline.** `scripts/eval -n 3` on each cloud provider; record it in `docs/dev/`. No new feature before it exists.
3. **Measured decisions.** `apply_patch` on and off for an OpenAI model (E6); `task` token savings and overuse; `grep` only if tally shows `bash rg`/`grep` dominate (E3).
4. **Then, by measured need:** `/compact`, image downscaling, and the deferred features in `REVIEW_FEATURES.md`: O5 process output, O6 web fetch, O7 steering, O8 ACP.

## Critical

None open.

## High

- [ ] Build the `bash` sandbox designed in `docs/dev/permissions.md`, before `[[tools.command]]`, the diff pager and MCP. Deferred: `~/projects/personal/sanduk` already runs agents in a disposable container with Seatbelt support; decide whether gilda reuses it before building its own.

### Feature gaps

- [x] **E1**: old tool results are elided past 70% of the window, and the model is warned at 85% (`agent/elide.go`).
  - [ ] Test whether Fable 5.1 and Opus 5.5 treat an elided tool result as edited history (`docs/dev/design.md`).
  - [ ] `/compact` (a summary) for sessions whose context is text, not tool output.

- [x] **E2**: sessions are saved after each prompt and resumed with `-c`, `--resume ID`; `--sessions` lists them (`app/session.go`).
  - [x] Two instances resuming the same session overwrote each other's saves. The second to save now forks.
  - [x] A REPL `/resume` and `/sessions`.
  - [ ] A session resumed with a different tool set (`--tools`, or a provider that drops `apply_patch`) replays calls to tools the request no longer defines. Unverified whether each API accepts that.

- [x] **E3**: `read` on a directory lists its entries.
  - [ ] Add `grep` only if `scripts/tally` shows `bash rg`/`grep` dominate.

## Medium

### Feature gaps

- [x] **E4**: `edit`, `write` over a file and `apply_patch` refuse a file not read since it last changed (`tool/seen.go`).
  - [ ] The check compares size and modification time; a same-size change within the filesystem's timestamp resolution passes.

- [x] **E5**: `read` returns images to models that accept them (`tool/read.go`, `llm.ToolResult.Images`).
  - [ ] Images over 3 MiB are refused, not downscaled.
  - [ ] Confirm against live APIs: images in Responses `function_call_output`, and OpenRouter forwarding a user message of images after tool messages.

- [x] **E6**: `apply_patch` for OpenAI models (`tool/patch.go`).
  - [ ] Measure with `scripts/tally` whether GPT models call it instead of editing through `bash`.

### Robustness

- [x] **F6** (low). Fixed: `llm.HeaderTimeout`. `-p` has no deadline for `anthropic`, `openai` and local providers: `llm.HTTPClient` (`llm/retry.go:46`) sets no `ResponseHeaderTimeout`. Fix: reuse the `streamClient` transport, or add `--timeout`. Unverified: whether the SDKs add their own timeout.

- [x] **F7** (low; exited groups are pruned at each bash call). `Jobs.Kill` (`tool/bash.go:224`) sends `SIGKILL` to stored group ids that are never pruned; a reused id hits an unrelated group. Fix: drop ids where `kill(-pgid, 0)` fails at each `bash` call. Closing the window fully needs `pidfd` on Linux.

- [x] **m4** (fsync and owner fixed; ACLs, xattrs and hard links still dropped): `target.write` does not `fsync` before the rename; the rename drops ownership, ACLs, xattrs and hard links (`tool/target.go:160-195`).

- [x] **m3** (fixed: `tool.EditMax`): `edit` reads the whole file with no bound (`tool/edit.go:126`).

- [x] **m5** (fixed: a payload of another type is rebuilt): unchecked `native.(sdk.MessageParam)` assertions panic on replay if two adapters share `name` and model (`llm/anthropic/anthropic.go:120`, `llm/openai/openai.go:120`, `llm/openrouter/openrouter.go:138`).

### Embedding (library use only)

- [x] **F5** (low; fixed: `tool.MinOutputCap`). `tool.Bash` panics when `Limits.OutputCap` < 1024 (`tool/bash.go:69`); the panic is in an `os/exec` goroutine and cannot be recovered. Fix: apply the 4096 minimum from `state/settings.go:137` in `Env.limits()`.

- [x] The TUI's agent goroutine sends on the event channel with no way out. If the TUI exits while the buffer is full, the goroutine blocks forever. Every send must also watch a context that lives as long as the TUI, not the per-prompt one, because Esc cancels that one and `doneMsg` must still be delivered.

- [x] The TUI's background commands, `Prepare` (`tui/tui.go:161`) and the model list (`fetchModels`, `tui/commands.go:143`), ran on the caller's context, which `tui.Run` neither cancelled nor waited for, so an embedding app could have network calls still running after the REPL closed.

### Permissions follow-ups

- [ ] `a` for a network tool covers every unlisted host, since an allowance is keyed on tool and reason kind. Consider keying `permission.Host` on the host too.
- [ ] Trust is keyed on the checkout's path. A trusted repository that later pulls a hostile `AGENTS.md` is not asked again. Consider storing a hash of the files.

### Testing

- [ ] Measure `task` with `scripts/eval`: does the main conversation use fewer tokens on `needle-in-many-files` and `answer-read-only`, and do models overuse it on small tasks? Add a task large enough to need elision.

- [ ] Run `scripts/eval` against each cloud provider, `-n 3`, and record the baseline in `docs/dev/`. Then answer E6: `apply_patch` on and off for an OpenAI model.

- [ ] Add `govulncheck ./...` to `make check`.
- [ ] `llm` at 62.9%; test that a `GILDA_LOG` exchange holds no header or body.
- [ ] No fuzz tests; add targets for `words`, `match`, `capture.Write`, `tool.Cap`.
- [ ] `cmd/gilda` reports 0.0%; use `go build -cover` with `GOCOVERDIR` ([Go docs](https://go.dev/doc/build-cover)).

### Gemini

- [ ] Run a multi-turn, tool-using task with a Gemini thinking model through OpenRouter, e.g. `-m openrouter:google/gemini-3-flash-preview`, to confirm its reasoning signatures survive the replay. See `docs/dev/dependencies.md`.

## Low

### Diff on `write` and `edit` approval

Done: `edit` and `write`, on by default, off with `diff = false` in `settings.toml` (`tool.Previewer`, rendered by `tui.ApprovalLines`). `-p` shows the diff too.

- [ ] Decide whether `diff` stays on by default after trying it.

- [x] `askVia` computes the preview before the TUI checks "always allow", so a call allowed with `a` still reads its file once for a preview nobody sees.

- [ ] Full-diff pager.

  Constraints:

  - A large diff fills the terminal history. Show a limited diff inline, say how many lines are hidden, and bind a key that opens the full diff in a pager. Hiding lines without a way to see them repeats the bug that `tui.ApprovalLines` fixed.

  - No token cost. The diff is computed locally and never sent to the model.

  Options: send the full diff to an external viewer, set by the user as with git's `core.pager`:

  - [delta](https://github.com/dandavison/delta): a pager for git and diff output, with syntax highlighting.

  - [hunk](https://github.com/modem-dev/hunk): a terminal diff viewer for reviewing changes written by agents, with split and unified layouts and pager support.

### Tools from the environment

See `docs/dev/tools.md`.

- [ ] Count tool calls per model with `scripts/tally` over `--json` runs before building `[[tools.command]]` declarations.

### Minor findings

- [ ] **m2**: `-P ollama` with the default endpoint still fetches OpenRouter's price list (`app/app.go:248`).
- [x] **m6**: REPL banner and status bar use `os.Getwd()`, not `Options.Root` (`tui/tui.go:498`, `:551`, `:588`).
- [ ] **m7**: `llm.HTTPClient` and the `SetLog` writer are package globals (`llm/retry.go:46`, `:121`).
- [x] **m8**: `/help` omits secrets and protected paths from the `auto` description (`tui/commands.go:58`).
- [ ] **m9**: `version` defaults to `0.1.0` despite Unreleased behaviour changes and the module rename (`cmd/gilda/main.go:26`).
- [ ] **m10**: `docs/dev/design.md:18` cites myra's `docs/dev/native-providers.md` with no link.

### REPL

From the TUI review of 2026-09-29.

- [ ] (E1, E2) `/export` (transcript to a file) and `/compact` (summarise older turns under context pressure). Design both on the same saved-conversation format as session resume, so neither needs redoing if resume is built.
- [ ] `model.allowed` reads the `always` map from the agent goroutine without a lock (`tui/tui.go`). This is safe only while tool calls run one at a time. Add a mutex if the agent starts running them in parallel.
- [ ] The `drive` test helper (`tui/commands_test.go`) reads `tea.Println`'s unexported `messageBody` field by reflection. Recheck it on a Bubble Tea upgrade.

### Deferred

- [x] (E2) When session resume is built, each adapter must serialise `llm.Message.Native` or reasoning replay is lost. Done: `llm.NativeCodec`.
