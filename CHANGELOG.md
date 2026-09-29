# Changelog

## Unreleased

### Added

- REPL: Ctrl-R searches earlier prompts in a picker and puts the choice in the input, unsent. `/copy` sends the last answer to the clipboard through the terminal (OSC 52), which also works over SSH; a terminal without OSC 52 support ignores it.

- The REPL aligns markdown tables and honours `:-:` and `--:` column alignment. A table is held until its last row, since column widths depend on every row; the live view shows it aligned so far. Lines starting with `|` and no separator row print as before.

- A trust question per checkout. When the mode is `auto` by default or from `settings.toml`, and the checkout has an `AGENTS.md`, gilda asks once whether to trust it and saves the answer in `state.json`. Declining uses `ask` mode there. A cloned repository could otherwise instruct an agent that runs `bash` unasked. An explicit `--permissions` skips the question, so scripts that set a mode keep working. A run with no one to answer uses `ask` and saves nothing. Embedding apps opt in through `App.Trust` and `App.SetTrust`.

- `bash_env` under `[tools]` names provider key variables that `bash` still receives; see Fixed.

- `--json` reports `"outcome": "truncated"` for an answer cut at `max_tokens`, with exit status 0; `-p` and the REPL print a warning. It was reported as `complete`. A new outcome was chosen over an error so callers that test only the exit status keep working. `agent.Result.Stop` carries the stop reason.

- `settings.toml` sets the limits that trade tokens, time or I/O, in `[agent]`, `[tools]`, `[prompt]` and `[prices]`: output tokens, round-trips, context window, stream resends, tool result size, read lines, bash timeouts, the write preview's read limit, AGENTS.md and skills in the system prompt, and the price list fetch. See Tuning in the README. `--max-tokens`, `--max-turns` and `--context` win over the file. Fixes and safety checks have no switch, since turning one off only brings back its bug. For embedding apps, `tool.Env.Limits`, `agent.Config.StreamRetries` and `agent.Config.OutputCap` take the same values, and `prompt.Build` takes a `prompt.Options`.

- The REPL shows the unified diff of an `edit` or `write` when it asks to approve it; `diff = false` under `[permissions]` in `settings.toml` turns this off. An edit's diff comes from the same code as the edit, so it covers the CRLF rewrite and `replace_all`. A write diffs against the file it replaces, or `/dev/null` for a new file; a binary file or one over 1 MiB gets a one-line summary, since `write` itself never reads the old file. Tools opt in through `tool.Previewer`.

- Permission modes: `--permissions auto|ask|all|read-only`, `GILDA_PERMISSIONS`, or `/permissions` in the REPL. The REPL asks inline, `-p` asks on the terminal, and `--json` refuses what would ask. A refused or declined call goes back to the model with the reason. No mode confines `bash`; only a kernel sandbox could.

- `~/.config/gilda/settings.toml`. Under `[permissions]`, `mode` sets the default mode, below `--permissions` and `GILDA_PERMISSIONS`. `secrets` and `protected` add paths that need approval. `commands` and `hosts` allowlist `bash` commands and network hosts. An unknown key or a malformed entry stops gilda, so a typo never drops a protection silently.

  ```toml
  [permissions]
  mode = "ask"
  secrets = ["*.pem", "!public.pem"]
  protected = ["migrations"]
  commands = ["go test", "git status$"]
  hosts = ["pkg.go.dev", "*.githubusercontent.com"]
  ```

- Built-in secret and protected paths, which nothing can lift. Secrets ask before any read or write: dotenv files, SSH private keys, key and key-store files, credential files such as `.netrc`, and Terraform state. Protected paths ask before a write: `.git`, `.hg`, `.svn` and `.jj`. Only names that nearly always mean credentials are built in, since a false positive could never be switched off.

- Path patterns follow `.gitignore`: a slash anchors to the working directory and covers what is under it, and `!` exempts an earlier pattern. The built-in rules, the settings file and an embedding app's rules are separate layers, and `!` exempts only within its own layer. A merged list would have let an app's `!*.pem` undo the user's `*.pem`.

- `commands` allowlists `bash` in `ask` mode by word prefix: `go test` allows `go test ./...`. An entry ending in `$` must match the whole command. A command that chains, substitutes, redirects or expands a variable never matches. `auto` does not use the list: there it would turn every other command into a prompt, and into a refusal under `--json`.

- Tools declare their effect, and permissions follow the declaration rather than the tool's name. `tool.ReadOnly` marks a tool that only reads local files. `tool.Paths` names the files a call touches. `tool.Hosts` names the hosts a call contacts. A tool that declares nothing is treated as modifying anything. Deciding by name would have let a custom tool called `read` inherit that built-in's policy.

- Network tools, declared by `tool.Hosts`. A call to listed hosts that writes no files runs in every mode; an unlisted host asks, or is refused in `read-only`. A network tool is never read-only, since a fetch can carry out what the model has read. `bash` is not checked against the list.

- `app.Options.Tools` adds custom tools. `tool.New` builds one from functions and declares only what its `Def` sets. Names must be unique and valid for every provider. `tool.HostOf` and `tool.Env.Abs` help implementations resolve hosts and paths as gilda does.

- `-P compat` talks to any OpenAI-compatible Chat Completions server, such as LM Studio or vLLM, at `--base-url` or `COMPAT_BASE_URL`, with an optional `COMPAT_API_KEY`. A named provider was chosen over accepting `--base-url` alone, which would have to guess the wire format.

- Retries show. The provider SDKs retry a failed request on their own and say nothing: Anthropic and OpenAI up to 4 times, the local providers twice, OpenRouter for up to a minute. The REPL's status bar and a `[retry]` line, `-p`'s stderr and a `retry` record in `--json` now report each retry and why the previous attempt failed. One HTTP transport counts the attempts each request makes, which covers all four adapters without hooking each SDK.

- `GILDA_LOG=FILE` appends each request's endpoint and status and the raw response stream, for diagnosing a provider. Request bodies and headers, keys among them, are never written.

- For embedding apps: `agent.Config.Approve` is asked before each call, and `permission.Approver` builds one from a mode. `agent.Record` maps events to JSON-ready records. `app.Options.Keys` takes vendor keys ahead of the environment, which a GUI app does not inherit. `StateDir`, `CacheDir` and `ConfigDir` keep an app's state apart from the CLI's.

- CI on GitHub Actions (`.github/workflows/ci.yml`) runs `make check` and the mock-provider smoke test `make run` on Linux and macOS, for pushes to `main` and pull requests.

- `go run ./scripts/tally run.jsonl ...` counts tool use in `--json` output per model: calls, failures and output bytes for each tool, with `bash` split by the programs a command runs. See `docs/dev/tools.md`.

### Fixed

- Tab after a paste kept cycling through the commands matching the text before it, since only a key press reset the cycle. It now restarts whenever the input differs from its last completion.

- A markdown table wider than the terminal wrapped mid-cell. It now drops its column padding instead; nothing is cut. `|` lines without a separator row get the same treatment when too wide. Task-list boxes (`- [ ]`, `- [x]`) are styled, and a double-backtick code span such as ``` ``a`b`` ``` is parsed.

- The REPL banner and status bar showed the process's working directory, not `app.Options.Root`, so an embedding app with another root showed the wrong one. `App.Root` exposes it.

- `/help` described `auto` without its secret and protected-path rules. It now states them, and that `a` never covers them.

- Backspace in a REPL picker removed one byte, so deleting a non-ASCII character left invalid UTF-8 in the filter. It now removes one character. Pickers also take PgUp, PgDn, Home, End, and Ctrl-U to clear the filter.

- A call already allowed with `a` still read its file for a diff nobody saw. The REPL now skips the preview for it.

- `tui.Run` now waits for its startup, model-list and switch requests before returning, as it does for the agent. An embedding app no longer has them running after the REPL closes.

- The REPL status bar overflowed and wrapped below about 60 columns, which moved the input box on every keystroke. Only the left side was cut to fit. A narrow bar now drops cost, effort and context use, then cuts the model id, then drops the permission mode. The mode goes last because it says what runs unasked.

- Leaving the REPL mid-turn could leave the agent goroutine blocked forever on a full event channel. Its sends now also stop when `tui.Run` returns, and `Run` waits for it. The per-prompt context was not enough: Esc cancels it, and the final result must still arrive.

- The REPL input showed its key-binding hint on every empty prompt. It now shows only before the first entry.

- The REPL showed the line still streaming as raw markdown, then restyled it when its newline arrived. It is now styled as it streams.

- `a` at an approval prompt approved every later call of that tool, secrets and protected paths included, for the rest of the session. It now covers one tool and one kind of reason, such as `edit` outside the working directory, and ends at `/clear` or `/permissions`. It is not offered for a secret or protected path. An edit to `.env` outside the working directory is now reported as a secret, not as outside, so an earlier `a` for outside paths does not cover it.

- `bash` received the provider keys from gilda's environment, so `echo $ANTHROPIC_API_KEY` put the key into the history. It no longer receives any provider's key variables or `GILDA_API_KEY`, unless `bash_env` names them. Key files on disk are still readable.

- Secret and protected patterns were case-sensitive. On macOS's case-insensitive filesystem, `read .ENV` and `write .GIT/hooks/pre-commit` ran without asking. Patterns now ignore case on every platform ([CVE-2014-9390](https://nvd.nist.gov/vuln/detail/CVE-2014-9390)); a false positive costs one prompt.

- Assistant text and reasoning reached the REPL unfiltered. A file holding escape sequences could, through the model's answer, set the window title, write the clipboard (OSC 52) or move the cursor. Control characters and bidirectional overrides now print as escapes; `-p` stdout is unchanged. Approval prompts also escape zero-width characters and bidirectional controls, which made a call display differently from what runs ([CVE-2021-42574](https://nvd.nist.gov/vuln/detail/CVE-2021-42574)).

- `-p` printed the approval label unescaped, so `\r` or an escape sequence in a command could overwrite what the user read before answering. It now prints the call as the REPL does, with its diff.

- `-p` printed a partial answer, then the whole answer after it, when a cut stream was resent. Plain-text stdout now gets each response's text when the response ends. Holding the text was chosen over a marker on stdout, which every consumer would have to parse.

- `--help` printed the value of `GILDA_API_KEY` as the default of `--api-key`. The flag no longer takes its default from the environment; the variable is read after parsing. `--api-key` itself now warns on stderr, since the key shows in the process list and shell history.

- A `write` or `edit` resolved its path twice: once for approval and again when it ran. A symlink swapped while the user decided could redirect it, and an edit could apply to content other than the diff shown. Both tools now resolve the file before approval and write to it through the directory opened then (`tool.Binder`). A call fails if the file or a directory above it was replaced, and an edit also if the content changed. Binding was chosen over re-checking before the run, which resolves the path again and only narrows the gap. `permission.AskFunc` and `App.Preview` take the bound tool, so the preview shows the file the call changes. A write over an existing FIFO or other non-regular file now fails instead of replacing it.

- `write` without `content`, or `edit` without `new_string`, succeeded and emptied the file or deleted the match. A missing or `null` string argument decoded as `""`. OpenAI tools are sent non-strict, and local servers may not enforce schemas, so the model's `required` list was no guarantee. Both are now errors that leave the file unchanged; an explicit `""` is still accepted.

- The approval prompt cut the call to `max(width-60, 20)` columns, so at 80 columns `$ echo harmless; rm -rf dir` showed as `$ echo harmless-l...`. The whole call is now printed above the prompt, wrapped, with control characters shown as escapes.

- `--base-url` was ignored when the provider was chosen automatically, from saved state or from the keys set. Prompts went to the vendor instead of the gateway. `--base-url` and `GILDA_BASE_URL` now need `--provider`, as `--api-key` does. Both checks now run after `-m PROVIDER:ID` is read, so that form also names the provider.

- `read` or `edit` on a FIFO waited for a writer before the regular-file check, and a cancel could not stop it. Files are now opened non-blocking and checked before reading. A `read` of a large file also stops on cancel.

- A refusal or content-filtered response from OpenAI, OpenRouter or a Chat Completions server ended the prompt as a success, often as "(no response)". Only the Anthropic provider mapped refusals. The other three dropped the refusal text and treated a `content_filter` stop as a normal end. gilda now shows the refusal text, answers any calls in a refused response with an error without running them, and fails the prompt. A filtered response can end mid-call, so running its calls could act on truncated arguments.

- A response cut off mid-stream ended the prompt with "stream ended without a finish reason", even with the timeout below fixed. gilda now sends the round-trip again, up to twice in a row, shown as a `[retry]` line; the cut response never enters the history. The SDKs retry a request that fails, not a response that stops partway. The error also names the read error that cut the stream, which the OpenRouter SDK's reader discards.

- OpenRouter streams longer than 60 seconds were cut off, and a prompt with a large context could time out before its first token and retry silently. The SDK's default HTTP client has a 60-second limit on the whole request, and its event reader drops the read error, so a cut stream reported only "stream ended without a finish reason". gilda now gives the SDK a client that bounds connecting and waiting for headers but not the stream.

- A model the provider does not list is refused, at startup and on `/model`, with a hint. `-m deepseek/deepseek-v4.1-flash` with `openai` suggests `openrouter:deepseek/deepseek-v4.1-flash`; before, it was accepted and failed on the first request. OpenAI's model list no longer offers models that cannot chat, such as `babbage-002` or `tts-1`.

- A tool-call chunk from OpenRouter with a negative index crashed gilda; it is now ignored.

- `-p -` with empty input and `--json` exited 1; it exits 2, like every other usage error.

- A custom tool whose schema was decoded from JSON lost its `required` list, because `ToolSpec.Required` accepted only `[]string`.

- A llama.cpp model id, which is a file path, got the hint to use `openrouter:`. The hint now applies only to cloud providers.

- A cost estimate uses the highest long-prompt tier the prompt passes. It took the last matching tier in list order, so a price list with tiers out of order priced a long prompt at a lower tier's rate.

### Changed

- The REPL status bar marks context use at 85% or more with `!` and a red background. Queued prompts are listed above the input, not only counted. Enter in a picker with no matches keeps it open.

- `permission.AskFunc` takes a `permission.Reason`, whose `Kind` says why the call asks. An app that remembers approvals needs it to avoid remembering one for a secret.

- By default gilda asks before a write outside the working directory, under version-control metadata or to a secret, and before reading a secret. 0.1.0 ran every call. With no one to ask, as under `--json`, such a call is refused. `--permissions all` restores the old behaviour.

- `--json` `tool_call` and `tool_result` records carry a `label` field.

- A tool line's arguments print dimmed, like `[tool]` and the outcome, so the answer stands out from the calls.

- Tool lines in the REPL and on `-p`'s stderr start with `[tool] `, as in myra, so they stand apart from the answer when read without colour. They stay one line, cut at the label so the outcome remains.

- `--help` wraps at 80 columns, or the terminal width if narrower, and names flag values (`--model ID`) in place of their Go types.

- Renamed from gila to gilda: the binary, the module path (`github.com/shakfu/gilda`), the `GILDA_*` environment variables, and the `gilda` config, state and cache directories. Old names are not read. Move `~/.config/gila`, `~/.local/state/gila` and `~/.cache/gila` to their `gilda` paths to keep settings, history and saved models.

## 0.1.0

### Added

- A coding agent and Go library with four tools: `read`, `write`, `edit` and `bash`. Providers `anthropic`, `openai` and `openrouter` go through their vendors' SDKs. `llamacpp` and `ollama` go through openai-go's Chat Completions.

- History is neutral, but each assistant message keeps the provider's own payload and replays it to the model that produced it. Thinking signatures, encrypted reasoning and `reasoning_details` survive the tool calls of a turn, and a mid-session switch still works. See `docs/dev/design.md`.

- A REPL on Bubble Tea v2, inline so output stays in scrollback. It streams markdown, shows one line per tool call, and pins an input box and a status bar with model, context used and session cost. `/model` and `/provider` open a filterable picker.

- Headless `-p`, and `--json` for one record per line, ending in a `result` record.

- Cost: reported by OpenRouter; estimated for OpenAI and Anthropic from OpenRouter's public price list, with cache rates and long-prompt tiers. Prompt caching on every provider that supports it.

- `AGENTS.md` from the config directory and from the repository root down to the working directory, and skills from the config directory.
