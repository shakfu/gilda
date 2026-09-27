# TODO

## Show the change when approving `write` and `edit`

Done: `edit` and `write`, on by default, off with `diff = false` in `settings.toml` (`tool.Previewer`, rendered by `tui.ApprovalLines`).

Remaining:

- [ ] `-p` asks on `/dev/tty` (`cmd/gilda/headless.go`, `ttyAsk`) and shows neither the diff nor escaped control characters. The escaping is a one-line call to `visible`; do it first (review F8).

- [ ] Decide whether `diff` stays on by default after trying it.

- [ ] `askVia` computes the preview before the TUI checks "always allow", so a call allowed with `a` still reads its file once for a preview nobody sees.

Constraints:

- A large diff fills the terminal history. Show a limited diff inline, say how many lines are hidden, and bind a key that opens the full diff in a pager. Hiding lines without a way to see them repeats the bug that `tui.ApprovalLines` fixed.

- No token cost. The diff is computed locally and never sent to the model.

Options:

- Send the full diff to an external viewer, set by the user as with git's `core.pager`:

  - [delta](https://github.com/dandavison/delta): a pager for git and diff output, with syntax highlighting.

  - [hunk](https://github.com/modem-dev/hunk): a terminal diff viewer for reviewing changes written by agents, with split and unified layouts and pager support.

## Tools from the environment

See `docs/dev/tools.md`.

- [ ] Count tool calls per model with `scripts/tally` over `--json` runs before building `[[tools.command]]` declarations.

## Gemini

- [ ] Run a multi-turn, tool-using task with a Gemini thinking model through OpenRouter, e.g. `-m openrouter:google/gemini-3-flash-preview`, to confirm its reasoning signatures survive the replay. See `docs/dev/dependencies.md`.

## Open findings from the self-review

- [ ] A response cut at `max_tokens` that has text but no calls counts as a success. `--json` reports `"outcome": "complete"` and the exit code is 0. Decide whether it is an error or a separate `"truncated"` outcome. Review recommends `"truncated"` with exit status 0: scripts can tell a cut answer from a whole one, and callers that test only the exit status keep working.

- [ ] `-p` in plain-text mode prints a partial answer, retries, and then prints the full answer after it on stdout. Decide whether to hold output until the attempt finishes, or to print a marker on stdout. Review recommends holding output: stdout is the answer, and a marker must be parsed by every consumer.

- [ ] The TUI's agent goroutine sends on the event channel with no way out. If the TUI exits while the buffer is full, the goroutine blocks forever. Every send must also watch a context that lives as long as the TUI, not the per-prompt one, because Esc cancels that one and `doneMsg` must still be delivered. This only matters when gilda is embedded.

- [ ] The TUI's background commands, `Prepare` (`tui/tui.go:161`) and the model list (`fetchModels`, `tui/commands.go:143`), run on `m.ctx`, the caller's context. `tui.Run` does not cancel it on exit and waits for none of them, so an embedding app can have network calls still running after the REPL closes. Give them a context that `Run` cancels, and wait for them before returning.

## Findings from the review of 2026-09-27

From `REVIEW.md` (commit `1156cd0`). Listed in the review's suggested order. F8 is under the diff section above.

- [ ] **F1** (medium). `a` at an approval prompt approves every later call of that tool, secrets and protected paths included. `m.always` is keyed on the tool name alone (`tui/tui.go:391`, `:222`). It survives `/clear` and `/permissions`, and contradicts the README's "nothing can lift them". Fix: key on tool and reason class, and never remember an approval for a secret or protected path; needs the reason passed to `AskFunc`. Add a test for the approval flow.

- [ ] **F4** (medium-low). Assistant and reasoning text reach the terminal unfiltered (`tui/tui.go:424`, `:439`, `:470`, `:524-527`). A file with escape sequences can, via the model's answer, set the title, write the clipboard (OSC 52) or move the cursor. Fix: pass both through `visible` before `md.line`. Leave `-p` stdout unfiltered.

- [ ] **F3** (medium on macOS). Secret and protected patterns are case-sensitive (`permission/permission.go:400`, `filepath.Match`). On a case-insensitive filesystem `.ENV` and `.GIT/hooks/pre-commit` bypass the check. See [CVE-2014-9390](https://nvd.nist.gov/vuln/detail/CVE-2014-9390). Fix: lower-case pattern and name on every platform. Unverified: whether APFS needs ignorable-code-point handling.

- [ ] **F2** (medium). `bash` inherits the provider keys (`tool/bash.go:70`, no `cmd.Env`). Fix: `os.Environ()` minus every `provider.Registry[].KeyEnv`, `GILDA_API_KEY` and `COMPAT_API_KEY`, plus a `[tools]` pass-through list. Decide the pass-through setting first.

- [ ] **F5** (low, library only). `tool.Bash` panics when `Limits.OutputCap` < 1024 (`tool/bash.go:69`); the panic is in an `os/exec` goroutine and cannot be recovered. Fix: apply the 4096 minimum from `state/settings.go:137` in `Env.limits()`.

- [ ] **F6** (low). `-p` has no deadline for `anthropic`, `openai` and local providers: `llm.HTTPClient` (`llm/retry.go:46`) sets no `ResponseHeaderTimeout`. Fix: reuse the `streamClient` transport, or add `--timeout`. Unverified: whether the SDKs add their own timeout.

- [ ] **F7** (low). `Jobs.Kill` (`tool/bash.go:224`) sends `SIGKILL` to stored group ids that are never pruned; a reused id hits an unrelated group. Fix: drop ids where `kill(-pgid, 0)` fails at each `bash` call. Closing the window fully needs `pidfd` on Linux.

Minor:

- [ ] m1: `visible` does not escape bidi controls (U+202A-202E, U+2066-2069) or zero-width characters (`tui/styles.go:158`). See [CVE-2021-42574](https://nvd.nist.gov/vuln/detail/CVE-2021-42574).
- [ ] m2: `-P ollama` with the default endpoint still fetches OpenRouter's price list (`app/app.go:248`).
- [ ] m3: `edit` reads the whole file with no bound (`tool/edit.go:126`).
- [ ] m4: `target.write` does not `fsync` before the rename; the rename drops ownership, ACLs, xattrs and hard links (`tool/target.go:160-195`).
- [ ] m5: unchecked `native.(sdk.MessageParam)` assertions panic on replay if two adapters share `name` and model (`llm/anthropic/anthropic.go:120`, `llm/openai/openai.go:120`, `llm/openrouter/openrouter.go:138`).
- [ ] m6: REPL banner and status bar use `os.Getwd()`, not `Options.Root` (`tui/tui.go:498`, `:551`, `:588`).
- [ ] m7: `llm.HTTPClient` and the `SetLog` writer are package globals (`llm/retry.go:46`, `:121`).
- [ ] m8: `/help` omits secrets and protected paths from the `auto` description (`tui/commands.go:58`).
- [ ] m9: `version` defaults to `0.1.0` despite Unreleased behaviour changes and the module rename (`cmd/gilda/main.go:26`).
- [ ] m10: `docs/dev/design.md:18` cites myra's `docs/dev/native-providers.md` with no link.

Testing gaps:

- [ ] `tui` at 18.5%; test `Update` with `approvalMsg` and key messages (F1).
- [ ] No fuzz tests; add targets for `words`, `match`, `capture.Write`, `tool.Cap`.
- [ ] `cmd/gilda` reports 0.0%; use `go build -cover` with `GOCOVERDIR` ([Go docs](https://go.dev/doc/build-cover)).
- [ ] `llm` at 62.9%; test that a `GILDA_LOG` exchange holds no header or body.
- [ ] No case-insensitive filesystem test for F3; skip unless the filesystem is case-insensitive.
- [ ] Add `govulncheck ./...` to `make check`.

Design:

- [ ] Put the `bash` sandbox in `docs/dev/permissions.md` ahead of `[[tools.command]]`, the diff pager and MCP. For models that read through `bash`, the secret list guards almost nothing in `auto`.
- [ ] Consider a per-repository trust prompt on first use: `auto` plus a cloned `AGENTS.md` lets the repository instruct an agent that runs `bash` unasked.
- [ ] When session resume is built, each adapter must serialise `llm.Message.Native` or reasoning replay is lost.

## CI

`.github/workflows/ci.yml` runs `make check` and `make run` on Linux and macOS.

- [ ] Check the first push on GitHub; the workflow has not run there yet.
