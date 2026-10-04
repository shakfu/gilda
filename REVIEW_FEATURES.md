# Feature review

Review of 2026-10-04 against `091c597`. Scope: feature completeness and gaps. It does not review correctness; `TODO.md` tracks that.

`docs/dev/features.md` already proposes 10 orthogonal features. This review does not repeat them. It cites them as `F#1`-`F#10` and challenges some of them in "Challenges to existing plans".

## Baseline

What gilda has, for reference:

- 4 tools: `read`, `write`, `edit`, `bash` (`tool/`).
- 6 providers, with native reasoning replay (`llm/`).
- 4 permission modes, secret and protected paths, command and host allowlists, a trust prompt per checkout (`permission/`).
- REPL with streaming markdown, approval diffs, history, Ctrl-R, `/copy` (`tui/`).
- Headless `-p` and `--json` with typed outcomes (`cmd/gilda/headless.go`).
- `AGENTS.md` and skills from the config directory (`prompt/prompt.go`).
- A library API: `agent`, `app`, `tool.New`, `permission.Approver`.

The design work is careful: permission layering, symlink-safe writes, and cache stability. The gaps are in **long-horizon work**: what happens after turn 30, after a crash, or after a wrong edit.

## Alternative framing

`features.md` asks "which features are orthogonal to the sandbox?". A different question is "which tasks fail today?". Four common task shapes, and where each one breaks:

| Task | Fails at | Gap |
|-|-|-|
| Refactor across 40 files | context fills; hard stop at 95% (`agent/agent.go:160`) | E1, E2 |
| Explore an unfamiliar repo in `read-only` | cannot list a directory or search | E3 |
| Fix a UI bug from a screenshot | no image input | E5 |
| CI job: "fix the failing test, then open a PR" | each `-p` starts empty; no `--continue` | E2 |

The ranking below follows that framing. Each item names a task that fails today.

## Essential gaps

Ranked by how often a common task hits them.

### E1. Context exhaustion is a hard stop

**Evidence.** `agent.Run` returns `ErrContextFull` at 95% (`agent/agent.go:160-166`). README: "There is no compaction." The only remedy is `/clear`, which discards everything. `TODO.md` defers `/compact`.

**Consequence.** One `cat` result can be 32 KiB, about 8k tokens. It is resent on every later request. On a 200k window, about 20 large results end the session. The model is not warned before the stop.

**Options**, cheapest first:

1. **Tool-result elision.** Replace old tool results, beyond the last N turns, with a stub such as `[result elided: 812 lines of go test]`. No LLM call. It breaks the cache once per elision, not per request. It is lossy, but tool output is the cheapest part to lose, since the model can re-run the tool.
2. **Provider-side context editing.** Anthropic offers context-editing and compaction features; OpenAI Responses has a `truncation` parameter. Exact names and status are unverified here. This is per provider, so it is uneven across 6 adapters.
3. **LLM-summary compaction** (`/compact`, or automatic at a threshold). The most general option. It costs one call. It interacts with `Native` replay: a summary has no signatures, so reasoning is dropped from that point.

**Recommendation.** Build option 1 first. It needs no format decision and no session-resume design. Add option 3 later on the transcript format (`F#1`). Also warn at 80%, so the model can wrap up before the stop.

**Question.** Is the 95% stop a deliberate cache-preservation choice? If so, elision trades one cache miss for a session that does not die. The README should state that trade.

### E2. No session persistence or resume

**Evidence.** `llm.Message.Native` is `json:"-"` (`llm/llm.go:43`). No flag resumes a session. `TODO.md` "Deferred" names the blocker.

**Consequence.**

- A crash, a closed terminal, or Ctrl-D loses the conversation. Only prompts survive, in `history`.
- `-p` cannot chain. A CI job cannot run "plan", then "implement", then "verify" in one context.
- `F#1` transcripts would record a session but could not replay it.

**Recommendation.** Treat `F#1` and resume as one feature with two halves. Serialise `Native` per adapter now; the format decision is the expensive part. Then add `--continue` (last session in this root) and `--resume ID`. A restored session whose provider or model differs should drop `Native`, as a switch does today.

**Risk.** A saved session holds whatever `bash` printed, keys included. Use mode 0600, as `history` does. Note it in the README.

### E3. `read-only` and `ask` cannot explore

**Evidence.** `read` refuses directories (`tool/read.go:117-125`). No `ls`, `glob` or `grep` tool exists. `read-only` refuses `bash` (`permission/command_test.go:58`). `ask` prompts for every unlisted `bash` command.

**Consequence.** In `read-only`, the model can read a file only if it already knows the path. It cannot list `src/` or find a symbol. `read-only` is the mode a reviewer or a cautious first run would choose, and there it cannot do the work. In `ask`, every `ls` and `rg` prompts unless allowlisted.

**Challenge to `tools.md`.** Option D is deferred until `scripts/tally` shows demand. The evidence there comes from two GPT runs that bypassed `read`. It says little about models trained on granular tools; Claude Code, Gemini CLI and Copilot CLI all ship `glob` and `grep` (per the table in `tools.md`). Also, demand for a tool the model does not have cannot be measured. The measurement shows how often the model reaches for `bash` to search; it cannot show whether a declared `grep` would be used.

**Recommendation.** Add one read-only `list` or `glob` tool. Let `read` on a directory return a bounded listing; that is cheaper still, needs no new schema, and keeps the tool count at 4. Add `grep` only if tally shows `bash rg` and `bash grep` dominate. Both are read-only by construction, so they run in every mode.

### E4. `edit` does not check what the model saw

**Evidence.** `tool.Binder` guards the window between approval and write (`tool/target.go:79`). Nothing checks that the model read the file, or that the file is unchanged since that read.

**Consequence.** Two failure modes:

- The model edits from memory of an old read. The user, a formatter, or a `bash` call changed the file since. If `old_string` still matches, the edit applies to text the model never saw in its current form.
- The model writes a whole file it never read. `write` replaces it, losing content.

**Recommendation.** Record `(path, mtime, size)` per `read` in the session. Make `edit` and `write`-over-existing fail with "file changed since last read; read it again" when the record is missing or stale. This is a small change in `tool/` and needs no new interface.

**Trade-off.** A strict check costs one extra `read` round-trip when a `bash` formatter touched the file. That is cheaper than a silent wrong edit.

### E5. Text-only messages

**Evidence.** `llm.Message` and `llm.ToolResult` carry strings only (`llm/llm.go:32-44`). `read` summarises binaries instead of returning them.

**Consequence.** There is no way to paste a screenshot, read a PNG, or check a rendered chart. All three cloud providers accept image input.

**Recommendation.** Add a content-part slice to user messages and tool results, with image parts first. `read` returns an image part for `png`, `jpg`, `gif` and `webp` under a size cap. Local providers without vision must degrade to the current summary. This touches every adapter, so do it before E2 fixes the serialised format. Otherwise the session format changes twice.

### E6. The edit format mismatches OpenAI models

**Evidence.** `tools.md`: two GPT runs made 0 `read` calls and read through `sed`, `nl` and `cat`. The cited explanation is training on `shell` plus `apply_patch`.

**Consequence.** For the `openai` provider, gilda's declared-tool advantages are lost: permission checks, output bounds, previews. GPT models may also route edits through `bash` (`sed -i`, heredocs), which `auto` runs unchecked.

**Recommendation.** Offer an `apply_patch` tool, selected per provider at session start. The tool list stays byte-stable within a session, so the cache holds. It declares `Paths` from the patch headers, so permissions and `Previewer` apply. Measure with `scripts/tally` before and after. This is the one case where the evidence already exists.

### E7. The `bash` sandbox

Already ranked High in `TODO.md`, with a design in `docs/dev/permissions.md`. It is essential: `auto` is the default, and in `auto` the secret list does not stop `cat .env`. This review adds nothing to that design.

## Orthogonal gaps not in `features.md`

Each is independent of the others and of the sandbox.

| # | Gap | Why it matters | Size |
|-|-|-|-|
| O1 | **Task list tool** (`todo`) | Long tasks drift. A model-maintained checklist shown in the status bar keeps it on track and shows progress to the user. Codex ships `plan`; Claude Code ships a todo tool (per the `tools.md` table). It is read-only by construction | S |
| O2 | **Subagents** | A `task` tool runs a child `agent.Agent` with its own history and returns only its final text. It isolates exploration from the main context, so it is an alternative to E1 as well as a feature. `agent.New` already makes this cheap | M |
| O3 | **Project skills and prompt templates** | Skills load only from the config directory (`prompt/prompt.go`). A repo cannot ship skills or `/commands`. The trust prompt already gates repo-supplied instructions, so the same gate covers these | S |
| O4 | **Date and VCS state in the system prompt** | The prompt has cwd, platform and shell only. With no date, the model reasons about "latest" versions from training data. Branch and dirty state save a `git status` call. Both are fixed per session, so the cache holds | XS |
| O5 | **Long-running processes** | `bash` has empty stdin, and background jobs are fire-and-forget (`tool/bash.go:123`). A dev server's later output is unreadable. A `bash` option that returns a job id, and a call that reads its new output, covers servers and watchers | M |
| O6 | **Web fetch tool** | The `Hosts` machinery (`permission/host.go`) exists but no built-in tool uses it. Docs lookup goes through `curl` in `bash`, which skips the host allowlist. A `fetch` tool with HTML-to-text and an output cap would be the first user of that layer | S-M |
| O7 | **Steering a running turn** | Enter queues a prompt until the turn ends (`tui/tui.go:338`). Correcting a wrong direction means cancelling. Injecting the queued text as a user note between tool rounds avoids losing the turn's work | S |
| O8 | **Editor integration via ACP** | Zed's Agent Client Protocol lets an editor drive an agent, with diffs and approvals shown in the editor. It fits gilda's event stream and `Ask` callback more closely than `F#10` MCP server mode, which exposes only tools | M-L |
| O9 | **Task-success evals** | `scripts/tally` counts calls, not outcomes. `tools.md` makes tool decisions conditional on measurement, but nothing measures whether a task succeeded. A small suite of scripted repo tasks with a check command, run over `--json`, would give E3, E6 and O1 a pass-or-fail signal | M |
| O10 | **Completion notification** | A terminal bell or OSC 9/777 when a long turn ends or an approval waits. The user has left the terminal by then | XS |
| O11 | **Headless tool subset and prompt override** | `-p` cannot restrict tools (`--tools read`) or append to the system prompt. `features.md` sets `--system` aside and puts tool subsets in profiles (`F#7`). A CI user needs them without writing a profile | S |
| O12 | **Windows** | README: Unix only, because `bash` uses process groups. Job objects are the Windows equivalent. Low priority unless the library targets GUI apps on Windows | L |

## Challenges to existing plans

1. **`F#4` `/undo` misses most edits.** It covers `write` and `edit` only. Under `auto`, much editing runs through `bash`. A git-based checkpoint covers both: `git stash create`, or a commit on a hidden ref per turn. It fails outside a repository and on ignored files. It is still the larger share of the value. Decide which share matters before building the `Undoer` interface.

2. **`F#6` parallel tools targets a cost that is too small.** Local `read` calls take milliseconds. The latency that matters is the provider round-trip, which parallel local tools do not reduce. Parallel *subagents* (O2) would.

3. **"Plan mode is `read-only` plus a prompt"** (`features.md`, set aside). It fails because of E3: `read-only` cannot explore. Fix E3 first, then the set-aside reasoning holds.

4. **The sandbox-first ordering blocks too much.** `TODO.md` puts the sandbox before `[[tools.command]]`, the diff pager and MCP. E1, E2, E4 and E5 do not widen what runs, so the ordering argument does not apply to them. They can land in parallel with the sandbox.

5. **The 95% threshold is invisible to the model.** Even before E1 is built, a system note at 80% ("context is nearly full; finish or summarise") costs nothing and lets a run end cleanly.

## Suggested order

1. E4 read-tracking, O4 date and VCS in the prompt, and the 80% warning. Each is small, and each prevents a silent failure.
2. E3 directory listing in `read`.
3. E1 tool-result elision.
4. E5 content parts, then E2 session format and resume, in that order, so the format changes once.
5. E6 `apply_patch` for OpenAI, measured with O9.
6. O1, O2, O3. O2 also relieves E1.
7. The sandbox (E7) runs in parallel with all of the above.

## Open questions

- Who is the primary user: an interactive developer, or CI and embedding apps? E2 and O11 rank higher for the second; E5, O7 and O10 for the first.
- Is the 4-tool minimum a goal or a starting point? E3, E6, O1 and O6 each add one tool. If the minimum is a goal, extending `read` (E3) and switching edit formats per provider (E6) keep the count.
- Does the 2026-08-31 edited-history restriction (`docs/dev/design.md`) also apply to E1 elision and E2 resume? Both change history. Test it against Fable 5.1 and Opus 5.5 before choosing an E1 option.

Inferences not verified in this review: exact provider context-editing APIs (E1 option 2), peer tool sets (taken from `tools.md`, which marks them unverified), and the ACP protocol's current scope (O8).
