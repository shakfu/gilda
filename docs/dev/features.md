# New features

Candidate features that are **orthogonal**: each touches a different subsystem, none depends on the `bash` sandbox, and none duplicates what is already designed. Written 2026-09-29 against `1156cd0`-era code (`TODO.md`, `docs/dev/`).

The bar for this list: a feature must (1) solve a problem gilda's own design docs name but leave to a different mechanism, (2) reuse an extension point that already exists (`agent.Event`, `tool.Tool`/`Binder`, `permission.Rules`, `app.Options`, `state.Settings`), and (3) not need the sandbox, MCP client or `[[tools.command]]` to land first.

## Not proposed here (already planned)

Listed so the rest is provably orthogonal:

| Planned | Where | Why a new feature must not assume it |
|-|-|-|
| `bash` sandbox | `docs/dev/permissions.md`, `TODO.md` High | Each item below is useful with the sandbox off, and none hooks into Landlock/Seatbelt or `pre_exec`. |
| `[[tools.command]]` | `docs/dev/tools.md` option B | Model-facing declared tools. Hooks (#5) and MCP server (#10) are user-facing or external, not new schemas. |
| MCP **client** | `docs/dev/tools.md` option C | Deferred until it is needed. #10 is the inverse (gilda as server) and shares no code. |
| Full-diff pager | `TODO.md` Low | #3's `/context` and #4's `/undo` are separate REPL surfaces. |
| Session resume + compaction | `docs/dev/design.md` "What was left out" | #1 is the transcript substrate, not resume. Resume stays deferred; #1 only makes it possible later. |
| Built-in `grep`/`glob` | `docs/dev/tools.md` option D | Model-facing; unmeasured demand. |

## Summary

| # | Feature | Surface | Effort | Depends on |
|-|-|-|-|-|
| 1 | Session transcripts | `--transcript`, `[session]` | M | `agent.Event`, adapters serialise `Native` |
| 2 | Cost and token budgets | `--max-cost`, `[agent] max_cost` | S–M | `agent.Usage`, `price.Catalog` |
| 3 | Context accounting | `/context` | S–M | a byte→token estimate |
| 4 | Turn checkpointing and `/undo` | `--checkpoint`, `/undo` | M–L | `tool.Binder`, `state` |
| 5 | Event hooks | `[hooks]` in `settings.toml` | M | `agent.Event`, `tool.Env.Hide` |
| 6 | Parallel read-only tools | `[agent] parallel_tools` | M | `tool.ReadOnly` |
| 7 | Named profiles | `[profiles.*]`, `--profile` | S–M | `app.Options`, `state` |
| 8 | `gilda doctor` / `gilda config` | subcommands | S–M | `state.LoadSettings`, `app` resolution |
| 9 | REPL ergonomics: Ctrl-R, `@file` | keys | S–M | `state.History`, `tui.picker` |
| 10 | MCP server mode | `gilda mcp` | M–L | `tool.Specs`, `permission.Approver` |

One constraint cuts across #1, #5 and #10: the system prompt and the tool list are byte-stable for a session on purpose (`prompt/prompt.go`, `docs/dev/permissions.md`). Any feature that adds or changes a tool's schema must do so at session start, not on the fly, or it drops the prompt cache.

---

## 1. Session transcripts

**Problem.** The only durable record is `~/.local/state/gilda/history` (the REPL's prompts) and `GILDA_LOG`, which writes the raw response stream and no request bodies or headers (`llm/retry.go` `SetLog`). Nothing captures the *neutral* conversation: messages, tool calls, results, usage, stop reasons. Resume is deferred partly because that substrate does not exist.

**Sketch.** `--transcript FILE` / `GILDA_TRANSCRIPT` / `[session] transcript`. Append JSONL:

- a `header` record: `version`, `provider`, `model`, `root`, `system`, `tools` (from `tool.Specs`), `started`;

- one record per `agent.Event`, reusing `agent.Record` (already JSON-ready and used by `--json`);

- a final `result` shaped like `headless.go`'s;

- a `native` field on assistant records: `{"provider","model","data":<opaque>}`.

Reuse the 0600 append mode of `GILDA_LOG`. Writing goes through `state.WriteFile` for the header and an `O_APPEND` handle for records so two instances never interleave a line.

**Work it forces.** `llm.Message.Native` is `json:"-"` on purpose; each adapter must marshal and unmarshal its own `MessageParam` / Responses items / `reasoning_details`. That is exactly the "Deferred" item in `TODO.md` ("When session resume is built, each adapter must serialise `llm.Message.Native`"). This feature pulls that task forward without building resume.

**Orthogonality.** Reads the event stream; changes no tool, permission or provider behaviour. It is not resume: nothing replays a transcript yet.

**Risk.** A transcript may hold secrets that `bash` printed (the sandbox doc is explicit that this is possible). It is a user-requested local file at 0600, same trust level as `history`; say so in the README, and do not enable it by default.

## 2. Cost and token budgets

**Problem.** `--max-tokens` and `--max-turns` bound one response and one prompt. There is no cap on a *session's* spend, and no way to run unattended with a stop condition. The `Cost` feature already totals usage and marks estimates `~`; it just never acts on them.

**Sketch.** `--max-cost USD` / `GILDA_MAX_COST` / `[agent] max_cost`, and optionally `[agent] max_input_tokens` for a token budget. Checked between round-trips, exactly like the 95% context guard in `agent.Run`:

- warn at 80% (REPL status bar, stderr under `-p`);

- at 100%, stop the loop and return a sentinel `ErrBudget`, sibling to `ErrContextFull`, with the same "keep completed turns" handling.

Report it as `outcome: "budget"` in `--json` with exit 0, or as an error with exit 1 — the project already chose a non-error outcome for `truncated`, so `budget` is the consistent choice.

**Orthogonality.** New `agent.Config` field and one check in the loop; no permission, tool or provider change.

**Risk.** Cost is unknowable until a round-trip finishes, so the budget can be exceeded by one turn; and `--base-url` disables estimates while local servers report none, so the cap only binds on cloud providers. Document both. Estimate visibly (`~`) as the usage line already does.

## 3. Context accounting (`/context`)

**Problem.** The window is refused past 95% with "start a new conversation" (`ErrContextFull`), but the user cannot see *what* filled it. `/cost` reports tokens and dollars, not composition.

**Sketch.** `/context` prints a breakdown:

- system prompt, split into base, each `AGENTS.md`, skills frontmatter, and tool definitions;

- history, by message, split into text / tool arguments / tool results, flagging the largest results (which the docs call the real token cost);

- the previous request's real `Usage.Input`, its cached share, and the 95% threshold.

To split the system prompt, refactor `prompt.Build` into a `prompt.BuildSections` returning named segments and keep `Build` as a thin wrapper, so nothing else changes. Tool definitions are `len(json.Marshal(tool.Specs(...)))`. History is a walk over `Agent.History`.

**Token source.** No tokenizer dependency is worth adding (`docs/dev/dependencies.md` rejects whole SDKs for less). Estimate bytes→tokens with a stated ratio, and anchor on the real `Usage.Input` from the last turn so the estimate is calibrated, not guessed.

**Orthogonality.** A read-only REPL command plus a small, backward-compatible prompt refactor. Pairs with, but does not require, compaction.

**Risk.** "Estimate" must be labelled as such, or a user will trust the wrong number. Keep it a view, not a decision input.

## 4. Turn checkpointing and `/undo`

**Problem.** Permissions catch *misdirected* writes but offer no recovery from a *correct* write that turns out wrong. `git` is not always clean, staged, or desired. This is the natural complement to the `Binder` design: the agent already resolves the exact file a `write`/`edit` will change, before it changes it.

**Sketch.** An optional interface beside `tool.Binder` and `tool.Previewer`:

```go
// Undoer is implemented by a tool whose call can be reversed. The agent keeps the closures in
// call order per turn and runs them in reverse for /undo.
type Undoer interface {
    Snapshot(args json.RawMessage) (undo func() error, err error)
}
```

`write` and `edit` snapshot the bound file (or record "did not exist") into `<StateDir>/checkpoints/<session>/<turn>N/`, capped by `Limits.DiffBytes`; over the cap the turn is marked non-undoable. `agent.Agent` keeps `[][]func() error` per turn and exposes `Undo(n)`. The REPL binds `/undo [n]` (default the last turn); restore refuses if a file changed since the snapshot (compare a content hash), so an external edit is never clobbered.

**Orthogonality.** Uses the same optional-interface pattern as `Previewer`/`Binder`; touches no permission logic. Works with the sandbox on or off.

**Risk.** `bash` writes are invisible to it — say so, exactly as the permissions doc says `bash` is unconfined. Disk growth: prune to the last N turns. Binary and large files: skip, and mark the turn as not fully undoable rather than silently partial.

## 5. Event hooks

**Problem.** The two moments a project always wants to automate — format after an edit, run tests when the agent stops — have no hook. A skill can *ask* the model to run them, but the model may not, and `[[tools.command]]` is the wrong shape: it is model-facing and costs tokens per request.

**Sketch.** `[hooks]` in the user's `settings.toml`, keyed by event, run through `os/exec` with a fixed `argv` (never a shell string):

```toml
[hooks]
after_edit = [["gofmt", "-w"]]      # the changed path is appended as an argument
on_stop    = [["go", "test", "./..."]]
on_retry   = [["notify-send", "gilda", "retrying"]]
```

A hook with `paths = true` receives the `tool.Paths` of the call that fired. Event data is passed by environment and stdin as JSON, never interpolated into `argv`, so a model-chosen filename cannot inject a command. Hooks run with the same filtered environment `bash` gets (`tool.Env.Hide`, `app.HiddenEnv`), bounded by `Limits.BashTimeout`, and their output is shown, not fed back to the model.

**Orthogonality.** Distinct from `[[tools.command]]` (user-facing, event-triggered, zero token cost, no schema, no permission-mode involvement). Consumes `agent.Event` like a second consumer next to the TUI.

**Risk.** Hooks are user code. v1 should read them only from the config dir, never a repository, so a cloned `settings.toml` cannot run a command; if repo-level config is ever added, gate it behind the existing `App.Trust` prompt. Run hooks synchronously and in order for `after_edit`; treat a failing hook as a note, not a turn failure.

## 6. Parallel read-only tool calls

**Problem.** `agent.runTools` runs every call strictly in sequence. A turn that reads five files pays five latencies for work with no ordering dependency.

**Sketch.** `[agent] parallel_tools = true` (default off). Only calls whose tool satisfies `tool.ReadOnly() == true`, is **not** a `tool.Binder`, and needs no approval run concurrently under an `errgroup`; everything else stays sequential. Results are reassembled in the original call order and emitted as one `llm.Message{Role: Tool}` (Anthropic requires a single tool message per assistant turn — already the shape here). Cancellation cancels the whole group.

**Orthogonality.** Confined to `agent.runTools`; no tool or permission change, because only tools that never ask and never write are parallelised.

**Risk.** A custom tool that lies about `ReadOnly` now races. Default off, and keep the `ReadOnly` contract as the single gate. Emit `ToolCall` before its matching `ToolResult` even when they interleave.

## 7. Named profiles

**Problem.** Switching between a big-model review session and a cheap local edit session means re-typing `-P`, `-m`, `--effort`, `--max-tokens`. `state.json` remembers only the last of each.

**Sketch.** `[profiles.review]` bundles provider, model, effort, limits, permission mode and an optional tool subset:

```toml
[profiles.review]
provider = "openrouter"
model = "anthropic/claude-opus-5"
effort = "high"
permissions = "read-only"
tools = ["read"]        # drop write, edit, bash for a reviewers-only session
```

`--profile NAME` and `/profile NAME` apply it; explicit flags win over the profile, the profile wins over `settings.toml`, which wins over defaults. Resolved in `app.New` before the current default chain. A tool subset must still pass `tool.Check`.

**Orthogonality.** Sits above the existing resolution in `app.New`; changes no tool or provider.

**Risk.** Precedence must be documented as strictly as `settings.toml` already is; a silent winner is the failure mode this project consistently avoids.

## 8. `gilda doctor` and `gilda config`

**Problem.** `settings.toml` fails loudly on an unknown key, but a *valid* file with the wrong value, a missing key, an unreachable local endpoint, or a stale price list is found only at the first request. There is no way to see the effective, merged configuration.

**Sketch.**

- `gilda config` prints every effective value with its source (`default`, `settings.toml`, `--flag`, env). This needs `app.New` to expose the merged view; add an `app.Describe()` on the resolution it already performs rather than re-deriving it.

- `gilda doctor` checks: each provider's key presence (`provider.Registry`, `App.HasKey`), local endpoint reachability (`GET /v1/models`, short timeout), config parse, trust status of the cwd, price-list age (`price.Load`), and `bash`/`go` on `PATH`. Exit non-zero when a named provider cannot run.

**Orthogonality.** New subcommands over existing loaders; no runtime path changes.

**Risk.** Never print a key: mask to a presence flag and a last-4. `gilda config` must not trigger network I/O.

## 9. REPL ergonomics: reverse search and `@file`

**Problem.** History browsing is Up/Down only (`state.History`), and mentioning a file means typing its path for the model to `read` — a tool round-trip and, in `ask` mode, a prompt for a read the user already intends.

**Sketch.**

- **Ctrl-R** reverse incremental search over `History.Entries`, the stored history already loaded at startup.

- **`@` mentions** open the existing `tui.picker` (fuzzy filter) over `git ls-files` in a repo, else a walk. The chosen file expands *client-side* into a bounded block (the `ReadLines` / `ReadLineBytes` caps) before send, so the read is the user's own action, not a tool call, and the permission layer is untouched. Document that `@` reads the file as the user.

**Orthogonality.** Pure REPL; the picker, styles and history already exist. User-facing, so it is outside `docs/dev/tools.md`'s model-facing scope (which already names the fzf picker as a user-facing integration).

**Risk.** `@` collides with emails and decorators in prose — only trigger at a word start and only offer completion when a path-ish token follows. Bound the expanded size and prove the cap.

## 10. MCP server mode (`gilda mcp`)

**Problem.** The deferred MCP *client* lets gilda call other servers. The inverse — letting an editor or another agent call gilda's four tools through gilda's own permission layer — is not planned at all, and is the cheaper half: gilda already owns `tool.Specs`, the `Tool` interface and `permission.Approver`.

**Sketch.** `gilda mcp [--tools read,write,edit,bash] [--read-only]` speaks MCP over stdio, maps each `tool.Tool` to an MCP tool (schema from `Spec()`), and routes each call through `permission.Approver`. With no one to ask, a would-ask call is refused — the same rule `--json` already uses. Default to `read-only`.

**Orthogonality.** Shares nothing with the MCP client beyond the protocol name. It reuses gilda's declared tools, so it does not hit the client's core problem ("every MCP tool is undeclared"), which is why it can land before, not after, the sandbox and the client.

**Risk.** MCP stdio lifecycle, and translating `ToolSpec.Schema` to MCP's `inputSchema` (mostly a rename). A long tool call needs progress forwarding so the client does not time out.

---

## Considered and set aside

- **Plan mode / `--dry-run`.** A mode that proposes writes without applying them is `read-only` plus a prompt nudge; `read-only` already refuses writes and feeds the reason back to the model. Fold it into a skill instead of new code.

- **Built-in `grep`/`glob`.** Already analysed in `docs/dev/tools.md` option D; demand is unmeasured. Not orthogonal to `bash`; skip until `scripts/tally` shows the calls.

- **Output compression (`rtk`).** `docs/dev/tools.md` step 5 already schedules an A/B; it is a measurement, not a feature to build now.

- **Provider failover.** Attractive, but `llm.Native` replay is per provider/model by design; a mid-turn failover would drop reasoning and is a decision, not an orthogonal addition.

- **Per-repository `.gilda/settings.toml`.** Genuinely orthogonal, but only safe behind the existing trust prompt; revisit after #5 establishes the config-source rule.

- **Persisting session cost across runs, shell completions, `--system` flag.** Small and useful, but each is a one-liner next to something above; not worth separate entries.

## Suggested order

1. **#2 budget** and **#3 `/context`** — small, self-contained, immediately useful, and they make long unattended runs legible.

2. **#1 transcripts** — unlocks resume and audits, and forces the adapter `Native` serialisation that the project already knows it owes.

3. **#4 `/undo`** and **#5 hooks** — the two highest-value "the agent changed my repo" features, both built on existing optional interfaces.

4. **#8 doctor/config**, **#7 profiles**, **#9 ergonomics** — quality of life, low risk.

5. **#6 parallel tools** and **#10 MCP server** — performance and reach; land after the sandbox per the project's own priority note, since both widen what runs.
