# Permissions and sandboxing

How gilda's permission modes compare with Antigravity CLI's, and a design for a `bash` sandbox. Written 2026-09-23 against gilda 0.1.0. Nothing below is implemented.

## Priority

Build the sandbox before `[[tools.command]]` ([`tools.md`](tools.md), option B), the diff pager and an MCP client. Some models read through `bash` alone: two GPT self-reviews made 0 `read` calls (`tools.md`). In `auto`, `bash` runs unasked, so for them the secret list guards almost nothing; `cat .env` ran without asking in the review of 2026-09-27. Each further permission rule adds little until `bash` is confined.

## Antigravity's model

From the Antigravity CLI documentation, `~/.gemini/antigravity-cli/settings.json` (excerpt supplied by the user, not re-checked):

| `toolPermission` | Behaviour as documented |
|-|-|
| `request-review` (default) | asks before any write, bash command or remote network call |
| `proceed-in-sandbox` | runs terminal commands in a sandbox; "safe" commands run, "risky" ones ask |
| `strict` | asks before every non-read operation |

A second key, `enableTerminalSandbox`, turns the sandbox on. The excerpt does not say what "risky" means, or how `strict` differs from `request-review`. Both are open below.

## Mapping onto gilda

| Antigravity | Nearest gilda | Gap |
|-|-|-|
| `request-review` | `ask` | gilda also runs `commands` and `hosts` allowlist entries without asking |
| `strict` | `ask` with empty allowlists | an embedding app's `Options.Rules` can still add allowlist entries |
| `proceed-in-sandbox` | `auto` | `auto` runs `bash` unconfined |
| none | `all`, `read-only` | |

The only real gap is confinement. The approval side already covers what the three modes describe.

## Two axes, not one list of modes

A mode answers "does this call need a human?". A sandbox answers "what can a running call reach?". Antigravity folds both into `toolPermission` and still keeps a separate `enableTerminalSandbox`, so combinations like `request-review` plus sandbox exist but have no name.

Recommendation: add the sandbox as a separate setting, not a fifth mode.

- `auto` + sandbox is `proceed-in-sandbox`.

- `ask` + sandbox confines what the user approves. That matters because the user approves a label such as `$ make test`, not what `make test` runs.

- `all` + sandbox is the unattended case: nothing asks, writes stay under the root. This is minima's only mode ([README](https://github.com/shakfu/minima/blob/b276433/README.md)).

- `read-only` refuses `bash` either way; see open question 3.

Alternative framing: a sandbox per mode, where `auto` always confines when the kernel supports it. Rejected: it either fails to start on older kernels (below) or falls back silently, and a silent fallback leaves the user believing a boundary exists.

## `strict`

Not worth a mode. It differs from `ask` only by ignoring allowlists. Users can already empty `commands` and `hosts`. The one gap is loosening entries from an app's `Options.Rules`; an app that wants strictness can simply not add them.

If "line-by-line transparency" means seeing the change before approving it, that is a prompt feature. Today `write` and `edit` ask with a label such as `edit main.go` (`tool/edit.go:35`). Showing the diff at the prompt would help every asking mode.

## Sandbox design

Most of this follows minima's [`docs/dev/root-sandbox.md`](https://github.com/shakfu/minima/blob/b276433/docs/dev/root-sandbox.md), which shipped Landlock and Seatbelt confinement on 2026-09-20. Differences for a Go implementation are marked.

### Reference implementation

minima at [`b276433`](https://github.com/shakfu/minima/tree/b276433), in `src/tools/bash.rs` unless noted:

| What | Where |
|-|-|
| Linux backend: Landlock ruleset built in the parent, applied in `pre_exec` | [`sandbox_command`, linux](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L408-L469) |
| macOS backend: SBPL profile passed to `/usr/bin/sandbox-exec -p` | [`sandbox_command`, macos](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L471-L517) |
| writable set: root, `$TMPDIR`, toolchain caches | [`writable_outside_root` and helpers](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L200-L384) |
| creating cache directories before confinement | [`create_caches`](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L243-L281) |
| note on a denied write | [`looks_denied`](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L386-L406) |
| startup check | [`preflight`](https://github.com/shakfu/minima/blob/b276433/src/tools/bash.rs#L524-L536) |
| path check for `write` and `edit` | [`confine_path`, `protected`](https://github.com/shakfu/minima/blob/b276433/src/tools/mod.rs#L138-L192) in `src/tools/mod.rs` |
| end-to-end escape tests | [`scripts/test_sandbox.py`](https://github.com/shakfu/minima/blob/b276433/scripts/test_sandbox.py), [`scripts/test_sandbox_macos.py`](https://github.com/shakfu/minima/blob/b276433/scripts/test_sandbox_macos.py) |

The macOS backend ports directly. The Linux one does not, since Go lacks `pre_exec`; see below.

### Policy

The intersection of what Landlock and Seatbelt both enforce:

- reads allowed everywhere;

- writes allowed under the root, `$TMPDIR`, `/dev/null` and the toolchain caches;

- `writable` entries from `settings.toml` add directories;

- inherited by every descendant, not liftable by the process, and checked by the kernel at `open`, so no time-of-check-to-time-of-use window.

Toolchain caches are required, not optional. minima measured an offline `cargo build` opening files under `$CARGO_HOME` with `O_RDWR|O_CREAT` on every run. The full list is in minima's CHANGELOG, `--sandbox` entry.

### Linux: Landlock

Go has no `pre_exec` hook, so minima's approach (restrict the child between `fork` and `exec`) does not carry over. The options:

| Approach | Cost |
|-|-|
| Re-exec: `bash.go` spawns `/proc/self/exe __sandbox <policy> -- bash -c CMD`; the helper locks its OS thread, sets `PR_SET_NO_NEW_PRIVS`, calls `landlock_restrict_self`, then `syscall.Exec`s bash | one extra `exec` per call; gilda's own process stays unconfined |
| Restrict gilda's own process | rejected: gilda writes `state.json`, `history` and the price cache outside the root |
| `bwrap` | minima rejected it: fails under Docker's default seccomp and Ubuntu 24.04's user-namespace restriction |

Re-exec is the recommendation. `syscall.Exec` keeps the PID, so `Setpgid` and the group kill in `tool/bash.go:77` still work. Landlock applies per thread, so the helper must restrict and `exec` from the same locked thread (inference from the kernel docs, not tested in Go).

`golang.org/x/sys/unix` v0.47.0 carries `SYS_LANDLOCK_*` and `LandlockRulesetAttr`, and is already an indirect dependency. [go-landlock](https://github.com/landlock-lsm/go-landlock) adds ABI negotiation, at the cost of one dependency.

ABI floor: 3 (kernel 6.2), as in minima. ABI 1 blocks cross-directory `rename`, breaking `mv` inside the root. ABI 2 does not handle truncate, so `: > file` works anywhere. Debian 12, RHEL 9 and Ubuntu 22.04 GA are below it.

### macOS: Seatbelt

Prefix the argument vector with `/usr/bin/sandbox-exec -p '<profile>'`. No re-exec is needed. `sandbox-exec` is deprecated but still present; minima's doc cites the status. minima also denies preference writes, `open` and signals outside the sandbox; gilda should copy that list.

### `write` and `edit`

They run in gilda's process, which stays unconfined. The existing checks in `permission.writes` already hold them to the root in `auto`. Under the sandbox they should refuse, not ask, a path outside the root plus `writable`, so both tools and `bash` share one boundary.

### Rules that do not transfer to `bash`

Landlock rules only add access. A grant on the root cannot exclude `.git` inside it, and a read grant on `/` cannot exclude `.env`. So:

| Rule | `write`, `edit`, `read` | sandboxed `bash`, Linux | sandboxed `bash`, macOS |
|-|-|-|-|
| writes outside root | checked | denied | denied |
| `protected` (`.git` ...) | asks | not enforced | enforceable with SBPL `deny` |
| `secrets` reads | asks | not enforced | enforceable with SBPL `deny` |
| network | `hosts` for network tools | open | open |
| provider keys in the environment | not applicable | removed, unless `bash_env` names them | removed, unless `bash_env` names them |

Enforcing `protected` and `secrets` on macOS only makes one setting mean different things per platform. Recommendation: leave them unenforced for `bash` on both, and say so.

### Escaping the sandbox

This is what "risky commands prompt" needs to mean. Classifying a command string as safe does not work: minima's doc shows `eval`, `$IFS` and substitution defeating any text check, and gilda's `permission/command.go` refuses to match those forms for the same reason.

Recommendation: run every command confined. When one needs more, the model sets a `bash` argument such as `unsandboxed: true`, and that call asks.

- It asks in every mode, `all` included. Otherwise `all` + sandbox equals `all`.

- It is refused where no one can ask, as under `--json`, and in `read-only`.

- A confined command that fails with `Operation not permitted` gets a note naming the sandbox and the argument. minima found models otherwise retry or reach for `sudo`.

The argument should be in the schema whether or not the sandbox is on. `/permissions` can change the setting mid-session, and changing the tool schema would invalidate the prompt cache.

### Degradation

If the sandbox cannot be installed (old kernel, Landlock not in the boot `lsm=` list, `sandbox-exec` missing), gilda refuses to start. Run one confined command at startup, as minima's `preflight` does, so the failure comes before the first turn. A mid-session `/permissions` switch runs the same check and refuses the switch.

### Network

Not in the first version. Landlock ABI 4 filters TCP `connect` by port only, not by host. Whether later ABIs cover UDP is unverified. Seatbelt can deny all network. Reusing `hosts` for `bash` would need a local proxy and a policy allowing only its port, and programs that ignore `HTTP_PROXY` would fail. Until then, a sandboxed `bash` can still send out anything it can read. The README warning about untrusted prompts stays.

### Settings

```toml
[permissions]
mode = "auto"
sandbox = true
writable = ["~/.opam"]
```

Plus `--sandbox` and `--writable DIR`. `writable` without `sandbox` is an error, not a no-op, since a user would read it as a grant. The `--json` `result` record gains `sandbox` and `writable` next to `permissions`.

## Cost

Estimated, not measured. minima estimated 200-260 lines for both backends in Rust. gilda adds the `__sandbox` helper and the escape argument, so roughly 300. The test suite is the larger cost: minima's `scripts/test_sandbox.py` checks the disk after real escape attempts (redirect, `cd ..`, symlink, background job), and runs on both platforms in CI.

## Open questions

1. Is Antigravity's `strict` only `request-review` without allowlists, or does it show diffs? The excerpt does not say.

2. Should `auto` suggest `--sandbox` when the kernel supports it, for example in the startup banner?

3. Could `read-only` + sandbox allow `bash` with no writable paths besides `$TMPDIR`? Only if network is also denied, since `read-only` refuses network tools to stop exfiltration. Most builds and tests would still fail on their cache writes.

4. Should the escape argument's approval be remembered per command prefix, as `a` remembers a tool for the session?
