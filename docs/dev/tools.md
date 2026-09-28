# Tools

What makes a tool built in, how gilda reaches programs in the environment such as `rg`, `fzf` or `quarto`, and a design for tools declared in `settings.toml`. Written 2026-09-23. Only the section "What exists today" is implemented.

## Terms

- **Built-in tool**: a `tool.Tool` compiled into gilda and sent to the model with every request. Today there are four: `read`, `write`, `edit`, `bash`.

- **Custom tool**: a `tool.Tool` an embedding app adds through `app.Options.Tools`, usually built with `tool.New`. Go code, not configuration.

- **Environment program**: anything on `PATH`. The model reaches it only through `bash`.

- **Model-facing** vs **user-facing**: a model-facing tool is one the model calls. A user-facing integration is one the human uses, such as a pager for approval diffs (delta, hunk) or a fuzzy file picker (fzf). They are separate questions; this document is about the first, except where noted.

## What a tool declaration gives gilda

A declared tool carries more than a name and a schema. Five things follow from the declaration, and `bash` gets none of them:

| Declared | Gives | Interface |
|-|-|-|
| effect | the permission modes decide without asking: a read-only tool runs even in `read-only` mode | `tool.ReadOnly`, `tool.Paths`, `tool.Hosts` |
| label | the approval prompt and tool line show what the call does, not a shell string | `Label` |
| preview | the approval shows the change before it happens | `tool.Previewer` |
| binding | the approval, the preview and the run act on the same file | `tool.Binder` |
| output shape | the tool bounds and formats its result, which sets its token cost | `Run` |

`bash` is opaque. In `ask` mode every command prompts unless the `commands` allowlist matches it. The allowlist matches by word prefix and rejects chaining, substitution, redirection and variable expansion (`permission/command.go`). In `read-only` mode `bash` is refused, allowlisted or not (`permission/command_test.go:58`).

## Evidence: models route around declared tools

In two self-reviews of this repository (2026-09-23), neither OpenAI model called `read`:

| Model | Reads through `bash` | `read` calls |
|-|-|-|
| `gpt-6-luna` | 21 calls: `sed -n`, `nl -ba`, `grep`, `find` | 0 |
| `gpt-6-astra` | `cat` on whole files, `grep`, `go doc` | 0 |

Consequences:

- **Permissions.** Each of those reads would prompt in `ask` mode, and be refused in `read-only` mode, where `read` would have run.

- **Tokens.** `read` stops at `read_lines`. `cat` returns the file up to `output_cap`, 32 KiB (about 8k tokens), and that result is sent again with every later request.

Two runs is a small sample. The explanation usually given is that OpenAI trains its models on `shell` plus `apply_patch`. That is a claim from third-party research, not checked here. So a new declared tool is only useful if models call it. Measure before adding one; see "Measuring" below.

## Tool count across agents

From third-party research the user supplied (citations not verifiable, counts version-dependent):

| Agent | Built-in tools | Style |
|-|-|-|
| GitHub Copilot CLI | about 18 | granular: separate glob, grep, view, edit, shell-session tools |
| Gemini CLI | about 17 | granular: read, read-many, glob, grep, list, write, replace |
| Claude Code | 15-23 | granular; the set depends on version and configuration |
| Cursor | about 12, more with browser | granular, plus a browser |
| Codex | 3-6 | primitive: shell, apply_patch, plan |
| gilda | 4 | primitive, plus `read` |

The count is a poor measure. `bash` reaches every program on the machine. What differs between agents is how much of the model's work goes through declared tools, where the permission modes and output bounds apply.

The token cost of a declaration is small. gilda's four definitions total 1,797 bytes of JSON (`read` 435, `write` 351, `edit` 558, `bash` 453). At about 4 bytes per token that is roughly 450 tokens; the ratio is an estimate, not measured. Definitions are cached after the first request, so each extra tool adds about 100-150 cached tokens per request. Tool results cost far more.

## What exists today

gilda can already use any program on `PATH`:

1. **The model runs it through `bash`.** `rg`, `quarto render`, `fzf --filter`: whatever the shell finds.

2. **AGENTS.md or a skill tells the model it exists.** A `~/.config/gilda/skills/quarto/SKILL.md` saying when and how to run `quarto render` is enough for the model to use it. A skill costs only its frontmatter on each request; the body is read when needed.

3. **The `commands` allowlist stops the prompts in `ask` mode.** `commands = ["rg", "quarto render"]`.

Limits of this path:

- `read-only` mode refuses all of it, including `rg`, which only reads.

- The allowlist is a prefix match on the command string. `rg --pre=sh pattern` matches `rg`, and `--pre` runs a program for each file. The allowlist trusts every flag.

- Output is whatever the program prints, capped at `output_cap`.

- The approval shows a shell string, not a structured call.

Embedding apps have a second path: `tool.New` with a `Def` that declares `ReadOnly`, `Paths` or `Hosts`. That needs Go code.

## Can a program be a tool at all?

Four properties decide it:

| Property | Needed because | `rg` | `fzf` | `quarto` |
|-|-|-|-|-|
| non-interactive | `bash` gives empty stdin and no terminal | yes | only with `--filter` | yes |
| bounded time | `bash_timeout`, default 120 s | yes | yes | a large render can exceed it |
| declarable effect | decides where the permission modes can run it without asking | read-only, if flags are fixed | read-only | writes its output files |
| useful exit codes | `bash` marks any non-zero exit `Failed` | exits 1 on no match | exits 1 on no match | 0 or failure |

`fzf` is mainly a user-facing tool. As a model-facing tool it is only `fzf --filter=QUERY`, a fuzzy filter over stdin, which `rg --files | fzf --filter=q` already reaches through `bash`. Its likely use in gilda is user-facing, e.g. a file picker for mentioning paths in the REPL.

## Options

### A. Keep `bash`; document the skill and allowlist pattern

No code. Everything above already works. It leaves `read-only` refusing read-only programs and the allowlist trusting every flag.

### B. Tools declared in `settings.toml`

The user declares a program as a tool. gilda builds it with `tool.New`, so it gets the same permission handling as a built-in tool.

```toml
[[tools.command]]
name = "rg"
description = "Search file contents with ripgrep. Returns matching lines as path:line:text."
argv = ["rg", "--line-number", "--no-heading", "--", "{pattern}", "{path}"]
read_only = true
ok_exit = [0, 1]        # rg exits 1 when nothing matches

[tools.command.args]
pattern = { type = "string", description = "Regular expression." }
path = { type = "string", description = "File or directory.", default = ".", path = true }
```

Rules the design needs:

- **No shell.** gilda runs `argv` with `exec`, not `bash -c`. A placeholder fills one whole argv element and cannot split into several, so `;`, `$()` and globs in a value are inert.

- **No injected flags.** A value starting with `-` could still set a flag: a `pattern` of `--pre=sh` would make `rg` run programs. Two defences: put `--` before the placeholders, as above, and reject values that start with `-` unless the argument sets `allow_dash = true`. `--` alone is not enough, because not every program honours it.

- **Declared effect is trusted.** `read_only = true` is the user's claim, like an entry in `secrets`. The command's argv is fixed, so the claim covers every call. An argument marked `path = true` feeds `tool.Paths`, so the secret and protected-path checks apply to it.

- **Exit codes.** `ok_exit` lists codes that are not failures.

- **Limits.** Output goes through `output_cap`, and time through `bash_timeout` or a per-tool `timeout`.

- **Names.** `tool.Check` already rejects duplicate and invalid names. A declared tool cannot replace a built-in one.

Cost: one definition per tool on every request, about 100-150 cached tokens each. Unknown: whether models call `rg` when `bash` can run it too. See "Evidence" above.

### C. MCP client

MCP (Model Context Protocol) lets gilda start or connect to servers that each expose tools. It gives access to many existing servers without writing a declaration per tool.

- **Tokens.** Servers often expose many tools with long descriptions. All are sent with every request.

- **Effect.** MCP tool annotations such as `readOnlyHint` come from the server, which gilda cannot verify. Each MCP tool would have to be treated as undeclared: it asks in `auto` mode and is refused in `read-only` mode, whatever it claims.

- **Complexity.** Server lifecycle, a transport, schema translation for three providers, and a dependency.

### D. Built-in Go implementations (`grep`, `glob`)

These have no dependency on `PATH`, give the same output on every machine, and are read-only by construction. They duplicate `rg` and still depend on models calling them.

## When a tool should be built in

Make a tool built in when most of these hold:

1. It is needed on every machine, whatever is on `PATH`.

2. The permission modes need its declared effect: it replaces `bash` calls that would otherwise prompt or be refused.

3. Its output shape affects token use enough to control.

4. Models are trained to call it, so they will use it.

`read`, `write` and `edit` meet 1-3. `bash` meets 1 and 4. A Go `grep` or `glob` meets 1-3. Whether it meets 4 is unmeasured. Everything else belongs in option A or B.

## Recommendation

1. **Measure first.** Count tool calls per model from `--json` output over a few real tasks: how often reading and searching go through `bash`, and which programs.

2. **Document option A now.** It works today at no cost. Add a "using programs on PATH" section to the README with a skill example and the `commands` allowlist.

3. **After the sandbox** ([`permissions.md`](permissions.md#priority)), **build option B if the measurements show `bash` reads and searches that would prompt or be refused.** It reuses `tool.New`, needs no new dependency, and fixes both `read-only` refusing `rg` and the allowlist trusting every flag.

4. **Defer option C** until a server is needed that option B cannot cover. If built, treat every MCP tool as undeclared.

5. **After the tool set is settled, A/B test output filtering.** [rtk](https://github.com/rtk-ai/rtk) compresses `bash` output per command, such as test failures only or one-line `git push`. Run the same tasks with and without it and compare input tokens, turns, failed `edit` calls and task success. Its filtering loses information, and `edit` needs exact text, so fewer tokens can still mean more round-trips.

6. **Treat user-facing integrations separately.** The diff pager in `TODO.md` and an fzf file picker fit there.

An alternative to B is teaching the permission layer about read-only programs: classify `rg PATTERN PATH` or `sed -n` inside `bash` as read-only. It needs no new tools, and it would catch what the GPT models did. It needs a per-program list of flags that write or execute (`sed -i`, `rg --pre`, `find -exec`), which is a larger and riskier surface than B's fixed argv.

## Measuring

`scripts/tally` counts tool use in `--json` output, per model:

```sh
gilda -p "task" --json > run.jsonl
go run ./scripts/tally run.jsonl [more.jsonl ...]
```

```
      model      tool  calls  failed  output bytes  share
  openai/m1  bash cat      4       0         61234    71%
  openai/m1      read      2       0         24810    29%
```

Each row is a tool, with `bash` split by the programs it ran: `bash cat` and `bash rg` count apart from `read`, and a call that runs several counts once under all of them, such as `bash nl+sed`. `output bytes` is the result text the model received, a proxy for its token cost; `share` is its part of that model's total. A run's rows go to the model in its closing `result` record, or to `unknown` if the run has none.

Limits:

- Only `-p --json` runs produce records. The REPL writes none, so the two self-reviews above were counted by hand.

- Programs are found by splitting the command at unquoted `;`, `&`, `|`, parentheses and newlines, skipping heredoc bodies, and taking each part's first word after keywords, assignments, redirections and wrappers such as `env`. So a loop counts its body: `for f in *.go; do echo $f; sed -n 1,80p $f; done` counts as `bash sed`. Builtins such as `echo` and `cd` count only when nothing else runs. This is not a shell parser: `eval`, `bash -c '...'` and `xargs` count under their own names, not the programs they run.

- Bytes are not tokens. At about 4 bytes per token the ratio holds for English and code, but not for every tokenizer.

## Open questions

1. Should option B allow `bash -c` templates, for pipelines such as `rg --files | fzf --filter={q}`? That brings back shell quoting, which option B exists to avoid.

2. Should a declared tool take `paths` from its output as well as its arguments? `quarto render` writes files the arguments do not name, so `auto` cannot check them.

3. Should gilda ship example declarations (`rg`, `fd`) in the README, or only the mechanism?
