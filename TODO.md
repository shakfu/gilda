# TODO

Items tagged F1-F8 and m1-m10 come from `REVIEW.md` (commit `1156cd0`, review of 2026-09-27). The review's own severity follows each tag.

## Critical

None open.

## High

- [ ] Build the `bash` sandbox designed in `docs/dev/permissions.md`, before `[[tools.command]]`, the diff pager and MCP.

## Medium

### Robustness

- [ ] **F6** (low). `-p` has no deadline for `anthropic`, `openai` and local providers: `llm.HTTPClient` (`llm/retry.go:46`) sets no `ResponseHeaderTimeout`. Fix: reuse the `streamClient` transport, or add `--timeout`. Unverified: whether the SDKs add their own timeout.

- [ ] **F7** (low). `Jobs.Kill` (`tool/bash.go:224`) sends `SIGKILL` to stored group ids that are never pruned; a reused id hits an unrelated group. Fix: drop ids where `kill(-pgid, 0)` fails at each `bash` call. Closing the window fully needs `pidfd` on Linux.

- [ ] **m4**: `target.write` does not `fsync` before the rename; the rename drops ownership, ACLs, xattrs and hard links (`tool/target.go:160-195`).

- [ ] **m3**: `edit` reads the whole file with no bound (`tool/edit.go:126`).

- [ ] **m5**: unchecked `native.(sdk.MessageParam)` assertions panic on replay if two adapters share `name` and model (`llm/anthropic/anthropic.go:120`, `llm/openai/openai.go:120`, `llm/openrouter/openrouter.go:138`).

### Embedding (library use only)

- [ ] **F5** (low). `tool.Bash` panics when `Limits.OutputCap` < 1024 (`tool/bash.go:69`); the panic is in an `os/exec` goroutine and cannot be recovered. Fix: apply the 4096 minimum from `state/settings.go:137` in `Env.limits()`.

- [ ] The TUI's agent goroutine sends on the event channel with no way out. If the TUI exits while the buffer is full, the goroutine blocks forever. Every send must also watch a context that lives as long as the TUI, not the per-prompt one, because Esc cancels that one and `doneMsg` must still be delivered.

- [ ] The TUI's background commands, `Prepare` (`tui/tui.go:161`) and the model list (`fetchModels`, `tui/commands.go:143`), run on `m.ctx`, the caller's context. `tui.Run` does not cancel it on exit and waits for none of them, so an embedding app can have network calls still running after the REPL closes. Give them a context that `Run` cancels, and wait for them before returning.

### Permissions follow-ups

- [ ] `a` for a network tool covers every unlisted host, since an allowance is keyed on tool and reason kind. Consider keying `permission.Host` on the host too.
- [ ] Trust is keyed on the checkout's path. A trusted repository that later pulls a hostile `AGENTS.md` is not asked again. Consider storing a hash of the files.

### Testing

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

- [ ] `askVia` computes the preview before the TUI checks "always allow", so a call allowed with `a` still reads its file once for a preview nobody sees.

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
- [ ] **m6**: REPL banner and status bar use `os.Getwd()`, not `Options.Root` (`tui/tui.go:498`, `:551`, `:588`).
- [ ] **m7**: `llm.HTTPClient` and the `SetLog` writer are package globals (`llm/retry.go:46`, `:121`).
- [ ] **m8**: `/help` omits secrets and protected paths from the `auto` description (`tui/commands.go:58`).
- [ ] **m9**: `version` defaults to `0.1.0` despite Unreleased behaviour changes and the module rename (`cmd/gilda/main.go:26`).
- [ ] **m10**: `docs/dev/design.md:18` cites myra's `docs/dev/native-providers.md` with no link.

### Deferred

- [ ] When session resume is built, each adapter must serialise `llm.Message.Native` or reasoning replay is lost.
