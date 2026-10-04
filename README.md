# gilda

A coding agent for the terminal, and a Go library for building one. It talks to each provider through that provider's own SDK: [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go), [openai-go](https://github.com/openai/openai-go) and the [OpenRouter Go SDK](https://github.com/OpenRouterTeam/go-sdk).

**IMPORTANT:** by default gilda runs `bash`, and writes inside the working directory, **without asking**. It asks before writes elsewhere and before touching secrets; `--permissions` changes what asks. In a checkout with an `AGENTS.md`, it first asks whether to trust it. See [Permissions](#permissions). No mode is a sandbox: whenever `bash` runs, it can read and write anything the user can. Run gilda in a container or a disposable checkout when the prompt or the repository is untrusted.

## Install

```sh
make install    # builds bin/gilda and copies it to ~/.local/bin
```

Requires Go 1.27. Unix only: `bash` relies on process groups.

```sh
% gilda -h
gilda is a coding agent. Without -p it starts a REPL; with -p it answers one
prompt and exits.

Usage:
  gilda [flags]

Flags:
  -P, --provider ID          ID: anthropic, openai, openrouter, llamacpp,
                             ollama, compat
  -m, --model ID             model ID, or PROVIDER:ID
  -p, --prompt PROMPT        answer one PROMPT and exit; - reads stdin
  -C, --root DIR             working DIR (default: the current one)
      --base-url URL         provider endpoint URL; needs --provider;
                             turns off cost estimates
      --api-key KEY          provider KEY; needs --provider; prefer
                             GILDA_API_KEY
      --permissions MODE     MODE for what runs without asking: auto, ask,
                             all, read-only (default: mode in
                             settings.toml, else auto). auto asks before
                             touching secrets and writing outside the root
                             or to protected paths
      --effort LEVEL         reasoning LEVEL: low, medium, high, xhigh, max
      --mock FILE            replay a scripted JSON conversation from FILE
      --max-tokens N         cap each response at N output tokens
                             (default: settings.toml, else 32000)
      --max-turns N          allow N provider round-trips per prompt
                             (default: settings.toml, else 64)
      --context TOKENS       context window in TOKENS (default:
                             settings.toml, else from the model list)
      --json                 with -p: print JSON lines, ending in a result
                             record
      --no-color             disable colour; also off when NO_COLOR is set
      --tools NAMES          offer only these tool NAMES, such as
                             read,bash (default: all)
      --append-system TEXT   add TEXT to the end of the system prompt
  -c, --continue             resume the newest session saved for the
                             working directory
      --resume ID            resume the session with this ID, or a unique
                             prefix of it
      --sessions             list the sessions saved for the working
                             directory and exit
      --refresh-models       refetch the price list, ignoring the cache
  -h, --help                 print this help
  -v, --version              print the version
```

## Providers

| `-P` | SDK and wire format | Key | Default model |
|-|-|-|-|
| `anthropic` | anthropic-sdk-go, Messages | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, or an `ant auth login` profile | `claude-opus-5` |
| `openai` | openai-go, Responses | `OPENAI_API_KEY` | `gpt-5.5` |
| `openrouter` | OpenRouter Go SDK, chat | `OPENROUTER_API_KEY` | `anthropic/claude-opus-5` |
| `llamacpp` | openai-go, Chat Completions | none; `LLAMACPP_BASE_URL`, default `http://localhost:8080/v1` | the server's first model |
| `ollama` | openai-go, Chat Completions | none; `OLLAMA_BASE_URL`, default `http://localhost:11434/v1` | the server's first model |
| `compat` | openai-go, Chat Completions | optional `COMPAT_API_KEY`; endpoint from `--base-url` or `COMPAT_BASE_URL`, required | the server's first model |

Selection:

- No `-P`: the provider last used, while it can still run (a cloud provider needs its key set); then the first of anthropic, openai, openrouter whose key is set. A local server, `compat` included, is reused only after you named it with `-P` or `/provider`; the fallback never picks one, so an endpoint you never chose is never tried silently.

- No `-m`: the model last used with that provider, then the default above.

- `-m provider:model` names both, for example `-m openrouter:openai/gpt-5.5` or `-m ollama:qwen3:8b`.

- A model the provider does not list is refused, at startup and on `/model`, with a hint: `openrouter:ID` for an id that belongs to OpenRouter, or the listed ids it resembles. A provider that lists no models is not checked. OpenAI's list leaves out models that cannot chat, such as embeddings, speech and image models.

- Provider and model are remembered only after a turn streams, so a mistyped model is not reused.

`compat` covers any other OpenAI-compatible Chat Completions server: LM Studio, vLLM, llama-swap, a machine on the LAN, or a hosted endpoint. For example, `gilda -P compat --base-url http://localhost:1234/v1`. Like the local presets, it is never chosen automatically and gets no cost estimate.

Start llama-server with `--jinja` so tool calls work.

## Features

- **Tools:** 5, and `apply_patch` for OpenAI models. `read` returns numbered lines, 2000 at most, with `offset` and `limit`, and summarises a binary file instead of dumping it. On a directory it lists the entries, sorted, with `/` after a directory and `@` after a symlink, so `read-only` mode can explore without `bash`. A PNG, JPEG, GIF or WebP file up to 3 MiB is returned as an image, for a model that accepts images: one OpenRouter's list marks so, or one of the `anthropic` or `openai` models it does not list. Other models get a note in its place, and the image stays in the history for a later model that does. Ask the model to `read` a screenshot by its path. `edit`, and `write` over an existing file, refuse a file that `read` has not returned in this conversation, or one whose size or modification time changed since, such as after a formatter ran through `bash`; the model reads it again. Each edit or write records the file it leaves, so consecutive edits need no read between them. `write` creates or replaces a file. `edit` replaces one exact string, or every one with `replace_all`. `bash` runs `bash -c` in its own process group, 120 s by default and 600 s at most. `apply_patch` applies a patch in the format OpenAI's Codex models write: files added, updated, moved or deleted, each update as hunks of context and changed lines, matched exactly, then ignoring trailing whitespace, then ignoring indentation. It writes nothing unless every file applies, and shows the diff of each file on approval. It is offered when the provider is `openai`, or `openrouter` with an `openai/` model, and fixed for the session; `apply_patch = true` or `false` under `[tools]` overrides that. `task` runs a subagent on a self-contained research question and returns its final answer, so its searches and file contents stay out of the conversation. The subagent uses the same provider, model and permission mode, with `read` and `bash` only and no knowledge of the conversation. Its tool calls show marked `[task]` and ask as the caller's do, and its tokens and cost count in the session. It cannot call `task` itself. `task = false` under `[tools]` turns it off. A result over 32 KiB keeps its first fifth and last four fifths.

- **REPL:** output goes to the terminal's scrollback. An input box and a status bar stay pinned below it. The bar shows the working directory, or a spinner, elapsed time and the current step, then the model, effort, context used and session cost. Assistant text streams as styled markdown, one line at a time; a table prints aligned once its last row arrives. Each tool call gets one line, such as `[tool] read main.go:1-80 -> 80 lines` or `[tool] $ go test -> exit 1: FAIL`. Each prompt ends with a usage line: context, tokens in with the cached share, tokens out, cost.

- **Headless:** `-p` streams the answer to stdout and everything else to stderr. `--json` prints one record per line: `start`, `turn`, `tool_call`, `tool_result`, `retry`, `elided` and `task`, then a final `result` with `outcome`, `text`, `error`, `turns`, `usage` and `context_used`. `outcome` is `complete`, `truncated` (cut at `max_tokens`), `error` or `cancelled`. Exit status 0 when complete or truncated, 1 on error, 2 on a usage error, 130 when cancelled. Plain-text stdout gets each response's text when the response ends, so a response resent after a cut stream prints once.

- **Network:** a response streams with no overall timeout, so a long answer is never cut off; Esc or Ctrl-C ends a stalled one. When a provider's SDK retries a request, the REPL's status bar and a `[retry] 1 after 503 Service Unavailable` line say why, as do `-p`'s stderr and a `retry` record in `--json`. Anthropic and OpenAI retry up to 4 times, the local providers twice, OpenRouter for up to a minute. A response cut off mid-stream is sent again by gilda itself, up to twice in a row; the cut response never enters the history. `GILDA_LOG=FILE` records each request's endpoint and status and the raw response stream, never request bodies or headers.

- **Cost:** OpenRouter reports each request's cost. For `anthropic` and `openai`, gilda estimates it from OpenRouter's public price list, marked `~`. It prices cached input at the cache rate and applies long-prompt tiers. The list is fetched without a key, at most once a day. `--base-url` turns estimates off, since a gateway need not bill at the vendor's rates. Local servers report no cost.

- **Token use:**

  - Anthropic requests carry a cache breakpoint on the system prompt and an automatic one on the last block.

  - OpenAI and OpenRouter requests carry a `prompt_cache_key`. OpenRouter also carries a `session_id` for sticky routing and `cache_control` for Claude models.

  - The system prompt is built once per session and never changes, so the cached prefix holds.

- **Reasoning:**

  - Each assistant message keeps the provider's own payload. That covers Anthropic thinking blocks with signatures, OpenAI encrypted reasoning, and OpenRouter `reasoning_details`.

  - The payload replays unchanged to the model that produced it. After a model or provider switch, the message is sent as text and tool calls.

  - `/thinking` shows reasoning as it streams.

- **Instructions:** `AGENTS.md` from the config directory, then every `AGENTS.md` from the repository root down to the working directory, are appended to the system prompt. The nearest comes last. Outside a repository only the working directory's file is read.

- **Skills:** `skills/<name>/SKILL.md` in the config directory, then `.agents/skills/<name>/SKILL.md` at the repository root, by the [Agent Skills](https://agentskills.io/specification) convention. The prompt lists each skill's path and frontmatter; the model reads the file when a task matches. The repository directory is named after `AGENTS.md`; other agents may look elsewhere, such as `.claude/skills`.

- **Environment:** the system prompt states the working directory, platform, shell, the date and the git branch, both as at session start. The branch is read from `.git/HEAD`, so no `git` process runs.

- **Sessions:** each conversation is saved after every prompt, to `$XDG_STATE_HOME/gilda/sessions/ID.json` at mode 0600. `-c` resumes the newest session for the working directory, and `--resume ID` a given one; a unique prefix of the id is enough. `--sessions` lists them. The REPL prints the id when it exits, and `--json` puts it in the `result` record. A resumed session keeps the provider and model unless `-P` or `-m` names others. Reasoning payloads are saved too, so a resumed turn replays them to the same model. A session saved in another directory is refused, since its history names files there. When two instances resume one session, the second to save continues under a new id and says so, so neither overwrites the other. The newest 100 sessions are kept. A session holds whatever the tools printed, secrets included; `save = false` under `[session]` in `settings.toml` turns saving off.

- **Cancellation:** Esc or Ctrl-C cancels a turn, including a pending request. Every tool call still gets a result, so the conversation stays valid.

- **Context:** the window comes from `--context`, the provider's model list, llama-server's `n_ctx`, or OpenRouter's list. Past 70%, tool results older than the last 4 tool messages are replaced by a one-line stub, `[gilda: elided N bytes of read output ...]`, and a `[context]` line says so. It is skipped when it would free under a tenth of the window, since each elision breaks the prompt cache once. Past 85%, the model is told once to wrap up. A request is refused once the last one filled 95%; `/clear` starts over. There is no summarising compaction.

### Permissions

The mode sets what runs without asking. The first that is set wins: `--permissions`, `GILDA_PERMISSIONS`, `mode` in `settings.toml`, then `auto`. `/permissions` changes it for the rest of a REPL session.

| Mode | Runs without asking | Asks before | Refuses |
|-|-|-|-|
| `auto` (default) | read-only tools; `bash`; writes inside the working directory; network calls to listed hosts | touching a secret; writes outside the working directory or to protected paths; unlisted hosts; tools that declare nothing | nothing |
| `ask` | read-only tools; allowlisted `bash` commands; network calls to listed hosts that write no files | everything else | nothing |
| `read-only` | read-only tools; network calls to listed hosts that write no files | nothing | everything else |
| `all` | everything | nothing | nothing |

The REPL asks inline: `y` allows the call, `n` or Esc declines it. `a` allows later calls of that tool for the same reason, such as `edit` outside the working directory, until `/clear` or `/permissions`. `a` is not offered for a secret or protected path, so the built-in patterns stay in force. `-p` asks on the terminal, even with its output redirected, and shows the call and its diff as the REPL does. `--json` has no one to ask, so a call that would ask is refused. A declined or refused call goes back to the model with the reason, and the `result` record names the mode as `permissions`.

#### What tools declare

Decisions follow what each tool declares, not its name, so a custom tool called `read` gains nothing:

- **Read-only:** the tool only reads local files, with no writes, processes or network requests. `read` is one.

- **Paths:** the files a call touches. `read`, `write` and `edit` declare them. Writes are checked against the working directory and the protected paths; reads and writes against the secrets.

- **Hosts:** the hosts a call contacts. A tool that declares them is a network tool and never read-only, since a request can carry out whatever the model has read.

- `bash` is recognised by its type.

- A tool that declares nothing is treated as modifying anything.

#### Secret and protected paths

Secrets ask before any read or write. Protected paths ask before a write. Built-in patterns always apply and nothing can lift them:

| List | Built in |
|-|-|
| secrets | dotenv files (`.env`, `.env.*`, except names containing `example`, `sample` or `template`); SSH private keys (`id_rsa`, `id_dsa`, `id_ecdsa`, `id_ecdsa_sk`, `id_ed25519`, `id_ed25519_sk`); keys and key stores (`*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`, `*.kdbx`); credential files (`.git-credentials`, `.netrc`, `_netrc`, `.pgpass`, `.pypirc`, `.htpasswd`, `credentials`, `credentials.json`); Terraform state (`*.tfstate`, `*.tfstate.backup`) |
| protected | `.git`, `.hg`, `.svn`, `.jj` |

Only names that nearly always hold credentials are built in, since a false positive could never be switched off. Noisier ones, such as `*.pem` (which also matches public certificates), `.npmrc` or `*.tfvars`, belong in `settings.toml`.

- A pattern without `/` matches a file's name for secrets, and any path component for protected.

- A pattern with `/` matches the path relative to the working directory and everything under it: `config/prod` covers `config/prod/db.yaml`.

- A leading `!` exempts what an earlier pattern in the same source matched; the last match wins, as in `.gitignore`. It cannot exempt a built-in pattern, so `"!.git"` has no effect, nor a pattern an embedding app adds.

- Paths are checked as written and after following symlinks, so a link to `.env` counts as `.env`.

- Case is ignored on every platform: `.ENV` is a secret and `.GIT/hooks` is protected. On a case-insensitive filesystem, such as macOS's default, they are the same files ([CVE-2014-9390](https://nvd.nist.gov/vuln/detail/CVE-2014-9390)).

#### Trust

An `AGENTS.md` in a cloned repository instructs the agent, and `auto` runs `bash` without asking. So when the mode is `auto` by default or from `settings.toml`, gilda asks once per checkout whether to trust its `AGENTS.md` files and `.agents/skills`, and saves the answer in `state.json`. Declining uses `ask` mode there, in this run and later ones. An explicit `--permissions` or `GILDA_PERMISSIONS` skips the question. With no one to answer, as under `--json` or without a terminal, the run uses `ask` and saves nothing.

#### Provider keys

`bash` does not receive the provider key variables, such as `ANTHROPIC_API_KEY`, or `GILDA_API_KEY`. A command could otherwise print a key into the history, which the next request sends to the provider, or to another after `/provider`. `bash_env` names variables to pass on anyway, for example to run a project's integration tests:

```toml
[tools]
bash_env = ["OPENAI_API_KEY"]
```

This removes the cheapest path to a key, not every one: `bash` can still read key files on disk.

#### Allowlists

`commands` allowlists `bash` in `ask` mode, where every other command asks. `auto` already runs `bash` without asking, and `read-only` refuses it.

- An entry is a word prefix: `go test` allows `go test ./... -run X`, with any further arguments. Choose entries whose arguments cannot start other programs: `go test -exec` can.

- An entry ending in `$` matches the whole command: `git diff$` allows `git diff` but not `git diff --output=x`.

- A command matches only if it is one simple command. Anything that chains, substitutes, redirects or expands a variable asks: `;`, `&&`, `|`, `$(...)`, backticks, `$VAR` or `>`.

`hosts` allowlists network tools. An entry is a host, or `*.` and a domain for any host below it: `*.githubusercontent.com` covers `raw.githubusercontent.com` but not `githubusercontent.com`. Ports and case are ignored. A call to listed hosts that also writes files is then judged by its paths.

Allowlists loosen rather than protect, so an entry from any source allows a command or host.

#### settings.toml

```toml
# ~/.config/gilda/settings.toml
[permissions]
mode = "ask"
secrets = ["*.pem", "!public.pem", "secrets/*"]
protected = ["go.sum", "migrations"]
commands = ["go test", "go vet", "git status$", "git diff$"]
hosts = ["pkg.go.dev", "docs.rs", "*.githubusercontent.com"]
diff = false
```

The REPL shows the unified diff of an `edit` or `write` below its approval prompt; `diff = false` turns this off. A `write` over a binary file or one over 1 MiB gets a one-line summary instead. A tool shows one by implementing `tool.Previewer`.

An unknown key or a malformed entry stops gilda with the file's path, so a typo never drops a protection silently.

#### Limits

These checks guard against mistakes, not an adversary.

- `bash` is not confined: `cat .env`, `rm -rf .git` or `curl` to any host through it are not caught, in any mode where it runs.

- `write` and `edit` resolve their file before approval and write to that file, failing if it or a directory above it was replaced since. A custom tool does the same only by implementing `tool.Binder`; otherwise a symlink swapped between the check and the write can redirect it.

- A host is checked by name, not by where it resolves or redirects.

### Tuning

`settings.toml` also sets limits that trade tokens, time or I/O. A key left out keeps the default. The `max_tokens`, `max_turns` and `context` keys apply only when their flags are not given.

```toml
[agent]
max_tokens = 32000     # output tokens per response
max_turns = 64         # round-trips per prompt
context = 200000       # window in tokens; default: from the model list
stream_retries = 2     # resends of a response cut off mid-stream

[tools]
output_cap = 32768     # bytes of one tool result
read_lines = 2000      # lines one read returns
read_line_bytes = 2000 # bytes kept of one line
bash_timeout = 120     # seconds, when the call sets none
bash_max_timeout = 600 # seconds a call may ask for
apply_patch = true     # offer apply_patch; default: to OpenAI models only
task = true            # offer the task tool, which runs a subagent

[permissions]
diff_max_bytes = 1048576 # bytes of the old file a write preview reads

[prompt]
agents_md = true       # AGENTS.md files in the system prompt
skills = true          # skills' frontmatter in the system prompt

[prices]
fetch = true           # OpenRouter's price list, fetched once a day

[session]
save = true            # save conversations for --continue and --resume

[repl]
notify = "bell"        # after a turn of 30 s or more: bell, osc9 or off
```

| Key | Affects |
|-|-|
| `max_tokens`, `max_turns` | tokens per prompt |
| `stream_retries` | tokens: each resend sends the whole request again, mostly as cache reads |
| `output_cap`, `read_lines`, `read_line_bytes` | tokens: each result stays in the history and is sent again with every later request |
| `agents_md`, `skills` | tokens on every request |
| `context` | when gilda stops a conversation as full, at 95% |
| `bash_timeout`, `bash_max_timeout` | wall-clock time |
| `diff_max_bytes` | local I/O when approving a write |
| `fetch` | one request at startup; off also drops cost estimates and, for OpenAI, the context window |

The system prompt and tool descriptions are fixed for a session, so these settings do not break prompt caching within one.

### REPL

| Key | Does |
|-|-|
| Enter | send; while a turn runs, queue the prompt |
| Shift-Enter, Alt-Enter, Ctrl-J | new line. Shift-Enter needs a terminal with the kitty keyboard protocol |
| Up, Down, Ctrl-P, Ctrl-N | history |
| Tab | complete a command; after `/model `, open the picker |
| Esc | cancel the turn |
| Ctrl-C | cancel the turn, else clear the input, else quit |
| Ctrl-D | quit on an empty line |

A turn that has run for 30 seconds rings the terminal bell when it ends or waits for an approval, so a user who switched away hears it. `notify = "osc9"` under `[repl]` sends a desktop notification instead, in terminals that support OSC 9, such as iTerm2 and WezTerm; under tmux it needs passthrough. `notify = "off"` turns both off.

| Command | Does |
|-|-|
| `/model [id]` | switch model; without an id, pick from the provider's list |
| `/models [filter]` | list the provider's models with their context windows |
| `/provider [id]` | switch provider; without an id, pick one whose key is set |
| `/effort [level]` | `low`, `medium`, `high`, `xhigh`, `max` or `default`; remembered |
| `/permissions [mode]` | `auto`, `ask`, `all` or `read-only`; without a mode, pick one |
| `/thinking` | show or hide streamed reasoning |
| `/clear` | new conversation, saved as a new session; session cost is kept |
| `/sessions` | list the conversations saved for this directory |
| `/resume [id]` | continue a saved conversation, keeping the provider and model; without an id, pick one |
| `/cost` | session tokens and cost |
| `/help`, `/exit`, `/quit` | |

## Library

The CLI is a thin layer over packages that can be used on their own:

| Package | Holds |
|-|-|
| `llm` | the neutral message, request, usage and `Provider` types |
| `llm/anthropic`, `llm/openai`, `llm/openrouter`, `llm/compat` | one provider per SDK |
| `llm/mock` | a scripted provider for tests |
| `tool` | the `Tool` interface and read, write, edit, bash |
| `permission` | permission modes, as an `Approve` function |
| `agent` | the tool loop, reporting typed events |
| `prompt` | the system prompt, `AGENTS.md` and skills |
| `price` | cost and context windows from OpenRouter's list |
| `provider`, `app`, `state` | the registry, session wiring and saved state the CLI uses |
| `tui` | the REPL |

```go
import (
	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/anthropic"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/prompt"
	"github.com/shakfu/gilda/tool"
)

jobs := &tool.Jobs{}
defer jobs.Kill() // stops background processes bash left running
a := agent.New(agent.Config{
	Provider: anthropic.New("anthropic", key, ""),
	Model:    "claude-opus-5",
	System:   prompt.Build(dir, ""),
	Tools:    tool.Default(tool.Env{Root: dir, Jobs: jobs}),
	// Nil Approve runs every call. permission.Approver builds one from a mode; the ask
	// function is called only for calls the mode does not settle.
	Approve: permission.Approver(permission.Auto, dir, func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string, why permission.Reason) (bool, error) {
		return confirm(label), nil
	}),
})
res, err := a.Run(ctx, "make the tests pass", func(e agent.Event) {
	emit(agent.Record(e)) // JSON-ready: text, reasoning, tool_start, tool_call, tool_result, retry, turn
})
```

`app.New` adds what the CLI does on top: provider and model selection, `Switch`, cost estimates and context windows. For an embedding app:

- `Options.Tools` adds custom tools to the four built-in ones; names must be unique and valid for every provider. A tool declares its effect, as in [What tools declare](#what-tools-declare), by implementing optional methods: `ReadOnly() bool`, `Paths(args) ([]string, error)` and `Hosts(args) ([]string, error)`. The `ReadOnly` claim is trusted, so it must hold for every call. A network tool should implement `Paths` too, returning no paths when it writes no files.

  `tool.New` builds a tool from functions and declares only what its `Def` sets; it rejects a `Def` that is both read-only and sets `Hosts`. `tool.HostOf` extracts a host from a URL, and `Env.Abs` resolves a path the way the built-in tools and the permission checks do:

  ```go
  count, err := tool.New(tool.Def{
  	Name:     "count",
  	ReadOnly: true,
  	Paths:    func(args json.RawMessage) ([]string, error) { /* the "path" argument */ },
  	Run:      func(ctx context.Context, args json.RawMessage) (tool.Result, error) { /* ... */ },
  })
  a, err := app.New(app.Options{Tools: []tool.Tool{count}})
  ```
- `Options.Rules` adds secrets, protected paths, commands and hosts. The built-in patterns, those in `ConfigDir`'s `settings.toml` and the app's `Options.Rules` are separate layers: a path needs approval when any layer matches it, and a `!` exempts only within its own layer. So an app cannot lift what the user's settings protect. `permission.Builtin()` lists the built-in patterns.

- `Options.Permissions` sets the mode; empty takes `mode` from `ConfigDir`'s `settings.toml`, then `auto`. `Options.Ask` sets how a call that needs approval asks; nil refuses such calls. It receives a `permission.Reason`; an app that remembers approvals should do so only where `Reason.Lasting()` holds. `SetPermissions` changes either later. `Trust` and `SetTrust` ask and record the trust question; an app that does not call them is never asked. The `agent` package alone applies no mode: its `Approve` defaults to running every call.

- `Options.Keys` takes vendor keys by provider id and wins over the environment. A GUI app launched from the desktop inherits no shell variables.

- `Options.StateDir`, `CacheDir` and `ConfigDir` keep its state, price list and `AGENTS.md` apart from the CLI's.

- `bash` inherits the process environment, minus `app.HiddenEnv`: the provider key variables. A macOS GUI app's `PATH` lacks Homebrew and toolchain directories, so set `PATH` at startup, for example from `$SHELL -lc 'echo $PATH'`.

- `Run` blocks, so call it from a goroutine and cancel it through its context. One `Run` at a time per `Agent`; read usage from `Response` events rather than from the `Agent` while a run is in flight.

Importing `agent`, `app` or the adapters links no TUI or CLI dependency.

## Build

```sh
make            # bin/gilda
make check      # gofmt, go vet, tests under -race; the full gate
make run        # one-shot against the mock provider
make repl       # REPL against the mock provider
make help       # every target
```

`go run ./scripts/eval -m MODEL evals/*` runs gilda on the scripted tasks in `evals/` and reports how many runs passed; `docs/dev/tools.md` shows A/B use.

Adapter tests run each SDK against a local server that replays server-sent events. They check the request gilda sends (cache markers, tools, reasoning replay) and the parsing of the stream, with no key or network. `cmd/gilda` tests build the binary and check exit codes and JSON records.

A mock script is a JSON array of responses, one per provider round-trip: `{"text", "reasoning", "calls": [{"name", "arguments"}], "usage", "stop", "error"}`. See `mock/`.

The binary is about 40 MB, mostly the three SDKs; startup takes about 10 ms. See `docs/dev/design.md`.

## Files

| Path | Holds |
|-|-|
| `$XDG_CONFIG_HOME/gilda/AGENTS.md`, `skills/` | your instructions and skills |
| `$XDG_CONFIG_HOME/gilda/settings.toml` | permission mode, secrets, protected paths, command and host allowlists, approval diffs, limits |
| `$XDG_STATE_HOME/gilda/state.json` | last provider, model per provider, effort |
| `$XDG_STATE_HOME/gilda/history` | REPL history, verbatim, mode 0600 |
| `$XDG_STATE_HOME/gilda/sessions/` | saved conversations, mode 0600; see Sessions |
| `$XDG_CACHE_HOME/gilda/openrouter-models.json` | the price list |

Unset XDG variables fall back to `~/.config`, `~/.local/state` and `~/.cache`.

| Variable | Meaning |
|-|-|
| `GILDA_PROVIDER`, `GILDA_MODEL` | defaults for `-P` and `-m` |
| `GILDA_BASE_URL`, `GILDA_API_KEY` | defaults for `--base-url` and `--api-key` |
| `GILDA_PERMISSIONS` | default for `--permissions`, ahead of `settings.toml` |
| `LLAMACPP_BASE_URL`, `OLLAMA_BASE_URL`, `COMPAT_BASE_URL` | local endpoints |
| `COMPAT_API_KEY` | optional key for `compat` |
| `GILDA_LOG` | file to append each request's endpoint and status and the raw response stream to |
| `NO_COLOR` | any value turns colour off |

## Licence

MIT.
