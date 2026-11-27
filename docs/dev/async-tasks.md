# Async tasks

`task` with `background: true` starts a subagent and returns at once. `task_collect` reads finished answers, waits for one, or cancels. Children can start children of their own, up to a depth limit. Status: built 2026-11-22 in the stages below; run live in "Measured" and through `examples/swarm` ("Embedding").

Prior art: minima's `-r` (`~/projects/personal/minima/docs/dev/recursion.md`) runs children as processes coordinated through files. prime-agent (`rlm.spawn`, `rlm.collect`, inbox notices) runs them under a daemon. gilda runs them as goroutines in its own process. That replaces minima's registry, `flock`, ledger, pid watch and stop markers with a `context` tree and channels.

## Model

- A **task** is one subagent: an `agent.Agent` with its own history, run on a goroutine.
- A **scope** (`agent.Tasks`) holds one agent's children. The root agent's scope lives as long as the `App`.
- A child's context derives from its parent's scope. Cancelling a scope stops its whole subtree.
- A child's answer reaches its parent only as `task_collect` output, never as user text. A child's text therefore never carries the user's authority. minima's first build put mail in the next prompt, and its review found that bug.
- A task finishes only once its own children have: a child runs `agent.Drive`, which waits for them.

## Tools

```text
task {prompt, background?, name?}
task_collect {ids?, wait?, cancel?}
```

- **`task`**
  - Without `background`, it runs synchronously, as before.
  - With `background: true`, it prints `started t3 "name"` and returns.
  - Ids are `t1`, `t2`, ... across the whole tree, so every tagged line is unambiguous.
- **`task_collect`**
  - Prints each finished answer not yet collected, then one status line per running task.
  - Named ids print again even after collection, so a second read sees the same answer.
  - `wait` (seconds, at most 600) blocks until at least one listed or running task finishes.
  - `cancel` stops those tasks and their subtrees first.
- `task_collect` is offered only with `task`, at session start, so the tool list stays fixed for the session.

## Delivery

The notice is gilda's own wording, never the child's text: `[gilda: t3 "scan" finished, 2 turns; read the answers with task_collect]`.

- **During a turn**, it is appended to the next tool result. No message is added, so the call/result pairing is unchanged.
- **After a turn**:
  - `-p`, ACP and every child run `agent.Drive`. While the agent has running tasks, `Drive` waits for one, sends the notice as the next prompt, and repeats. It returns once the agent answered and no task is left.
  - The REPL returns control to the user. When a task finishes while the REPL is idle, it starts a turn with the notice. `deliver = "prompt"` under `[tasks]` holds notices for the next prompt instead.
- A task stopped by `cancel` or `/tasks cancel` starts no turn: whoever stopped it knows. It is named in the next notice that does go out.

## Limits

`[tasks]` in `settings.toml`:

| Key | Default | Bounds |
|-|-|-|
| `depth` | 2 | How deep tasks nest. 1 is the old rule: a subagent cannot start one. |
| `running` | 4 | Tasks running at once across the tree. A `task` call past it fails and names the running ones. |
| `budget` | none | Tokens, input and output, all tasks of a conversation may use, at least 10,000. `/clear` starts it again. The conversation that starts the tasks is not counted. |
| `deliver` | `auto` | `auto` or `prompt`; see Delivery. |

Refusing a call past `running` keeps the model informed. Waiting instead would hold the parent's turn with no sign of why.

### Budget

Each task request reserves before it is sent, through `agent.Config.Reserve`, and settles after, as minima's ledger does, but under a mutex instead of `flock`:

| Outcome | When | The request |
|-|-|-|
| Granted | the budget less spent, held and the input estimate leaves at least 1,024 | holds input + grant, and goes out with the grant as `MaxTokens` |
| Waits | only other requests' holds stand in the way | waits for one to settle, or for its context |
| Fails | spent alone leaves less than input + 1,024 | returns `ErrBudget`; the task fails with that as its answer |

- The input estimate is the request's text at 4 bytes a token, or the last request's prompt and output, whichever is more.
- A completed request settles at its reported input plus output. One that did not complete (cancelled, failed) is charged its whole hold, since its real use is unknown.
- gilda sends `MaxTokens` on every dialect, so the provider enforces each grant. A response that needs more than its grant ends at the output limit.
- A failed task's scope closes, so its own tasks stop. Its parent, if a task, fails its next request too: the tree stops top-down, and the conversation reads the failures through `task_collect`.
- `task_collect` and `/tasks` end with `task budget: SPENT of LIMIT tokens spent`.

## Children

- Same provider, model and effort as the agent that starts them, copied at spawn. With `--task-model ID` or `[tasks] model`, tasks run on that model of the same provider instead, with its own window and image support and the provider's default effort. A task model is off by default. After a `/provider` switch, tasks follow the conversation's model again, since the task model was checked against the first provider's list.
- The permission mode is read at each call (`App.approve`), so a `/permissions` change reaches running tasks too.
- `read` and `bash`, plus `task` and `task_collect` while depth allows. No editing tools.
- A read record of their own, so a parent cannot edit a file only a child has read.
- Approvals go through the caller's `Approve`, so the user approves a child's calls as their own. `spawn` sets it after `TaskOptions.Build` returns, so a `Build` that leaves `Approve` nil cannot run its calls unasked. `OwnApproval` keeps `Build`'s instead, as does a caller with no `Approve`.
- `read-only` mode refuses `task`: it declares only that it writes no files. A child there would have `read` alone, and each starts paid requests without asking. `task_collect` is read-only.
- A panic in a child becomes that task's error.

## Usage

- A synchronous task's usage adds to its caller's run, as before.
- A background task's usage adds to its scope when it finishes. `Drive` includes the scope's usage in its result, so a child's total covers its own background children.
- `TaskDone` events carry each finished task's usage. Events from inside a task arrive wrapped in `TaskEvent`; frontends count usage only from unwrapped `TaskDone`, so nothing counts twice.

## Frontends

- **REPL**
  - Background events and every approval arrive on one channel that lasts the whole REPL. The per-run channel closes after each run, so a child that outlived the run could not use it.
  - Approvals queue; the TUI held one at a time, and a second overwrote the first.
  - The "always" allowances take a lock, since background children check them at any time.
  - Esc cancels the turn, not background tasks. `/tasks` lists them; `/tasks cancel ID` stops one. `/clear` cancels all of them. Quitting with tasks running asks for a second Ctrl-D or Ctrl-C.
- **`-p`**: runs `Drive`. Text output tags a task's tool lines `[t3]`. `--json` adds a `task` field to records from inside a task.
- **ACP**: runs `Drive`, so a prompt's response waits for its tasks. `session/update` reads the run's state, so task events are only logged; usage reaches the client with the next response. Permission requests are sent one at a time.

## Not done

- **Resume.** Tasks are not saved: goroutines cannot be. After `--resume`, `task_collect` says an earlier id ended with gilda. New ids start above every id the history names, so an old `t1` is not reused.
- **Partial usage on cancel.** A cancelled request is charged its whole hold against the budget.
- **Live runs.** Only Haiku 5.5 is measured; other models may choose differently. `Drive` stops a headless run from ending before its tasks; the REPL relies on the tool description.

### Measured

`go run ./scripts/eval -m MODEL -n 3 evals/fan-out-*`, 2026-11-22. Results in `evals/results/20261122-111447` (Haiku), `-113701` (Haiku, `fan-out-delegate` after the fix below), `-113803` (Sonnet) and `-115214` (GPT). Costs are per run; `scripts/eval` printed the total over all runs under "cost" until it was fixed, and these were recomputed from `results.json`.

| Model | `research` pass | `delegate` pass | `slow` seconds | Tasks started unasked |
|-|-|-|-|-|
| Haiku 5.5 | 3/3 | 2/3, then 2/3 | 23, 83, 83 | 0 of 6 runs |
| Sonnet 5.5 | 3/3 | 3/3 | 34, 34, 25 | 0 of 6 runs |
| GPT-5.5 | 3/3 | 3/3 | 87, 87, 87 | 0 of 6 runs |

- **No model started a background task unasked**, in 18 runs. On `fan-out-slow`, Sonnet ran the probes as `bash` jobs in 3/3 (twice with a fixed `sleep 30` rather than `wait`), Haiku in 1/3, GPT in 0/3.
- **Asked, every model drove them**: in all 12 `fan-out-delegate` runs, four tasks started and every answer was read through `task_collect` before the answer.
- **Delegating cost far more** than answering directly, `delegate` against `research`, per run:

  | Model | Input tokens | Cost | Seconds |
  |-|-|-|-|
  | Haiku 5.5 | 79,692 / 13,930 (5.7x) | $0.0086 / $0.0012 | 17 / 7 |
  | Sonnet 5.5 | 46,739 / 9,748 (4.8x) | $0.0927 / $0.0135 | 18 / 6 |
  | GPT-5.5 | 79,025 / 9,466 (8.3x) | $0.3355 / $0.0436 | 37 / 16 |

- The first Haiku failure was the eval's: `maxRetries` was declared but never read, and the tasks said so. The repositories now retry through `Do`.
- The two tools' schemas take about 435 tokens of every request, called or not.

Every run until `20261122-140557` ran a `bin/gilda` built before the later changes, since `scripts/eval` ran that binary and nothing rebuilt it. They measure the first description. `scripts/eval` now builds gilda from the source unless `-bin` names a binary. On the current code (`-140924`), `task`'s description no longer promotes parallel use, and Haiku still started no task unasked on `fan-out-research` (3/3).

#### The root decides

Haiku on `fan-out-delegate`, fixed repository, all runs (`-113701`, `-130652`, `-132210`, `-134205`, `-140557`): 19 of 27 passed. All 8 failures had "attempts" in every brief, as in "total attempts: initial attempt plus retries", and answered one high.

The briefs are not what separates the models. Briefs with "attempts":

| Root | Briefs with "attempts" | Pass |
|-|-|-|
| Haiku 5.5 | most, in passing runs too | 19/27 |
| Sonnet 5.5 | 12 of 12 | 3/3 |
| GPT-5.5 | 0 of 12 | 3/3 |

Tasks given such a brief report both counts, such as "7 retries (8 total attempts)", or only the attempts. A Sonnet root reads the retries out of either; a Haiku root sometimes takes the attempts. So the root's reading of its tasks' answers decides, more than the brief.

Quoting the user's request at the top of each task's prompt (`-140557`, 5/6) did not stop this, and one task, seeing the whole request, considered starting tasks for the other services. It was removed.

#### A strong root with cheap tasks

`-m anthropic:claude-sonnet-5-5 -settings FILE -n 6`, with `model = "claude-haiku-5-5"` under `[tasks]` (`-142535`):

| Root / tasks | Pass | Cost per run | Seconds |
|-|-|-|-|
| Haiku / Haiku | 19/27 | $0.0071 to $0.0086 | 17 |
| Sonnet / Haiku | 6/6 | $0.0327 | 17 |
| Sonnet / Sonnet | 3/3 | $0.0927 | 18 |

- The Sonnet root's own requests were $0.0281 of the $0.0327 and 18,662 input tokens; the four Haiku tasks were $0.0047 and 41,250 tokens.
- All 24 briefs said "attempts". 23 of the 24 task answers gave both counts and one gave only the retries, and every run answered with the retries.
- So Sonnet with Haiku tasks kept Sonnet's pass rate at 35% of its cost, on 6 runs.

## Embedding

`examples/swarm` drives `agent.Tasks` without `app`: its own `Build` through `agent.TaskConfig`, its own `Pricer`, and a check after each run that tokens, cost and `TaskDone`s add up across every agent. On 2026-11-27, Sonnet 5.5 in `auto` mode, on a copy of the repository, with a brief asking each part for two background tasks:

- 2 of 3 parts started 2 tasks each and collected both; all 4 finished in 5 turns. The part about tasks started none.
- Every check held: 39 responses, 4 in flight at most, 440,039 input tokens.
- The same brief in `read-only`, without the task request, read 1,615,950 input tokens: with `bash` refused, agents read whole files. One run each, so this is not a measurement of either mode.

## Stages

All four are built, with the budget, resume ids, live permissions and the eval task after them; `make test` and `go test -race ./...` pass.

1. `agent`: `Tasks`, `Drive`, `task_collect`, notices, depth and running caps (`agent/tasks.go`, `agent/task.go`).
2. `app` and `-p`: settings, the child builder, usage, `Drive` in headless runs.
3. REPL: the session channel, the approval queue, delivery, `/tasks`.
4. ACP: `Drive`, a logging sink, one permission request at a time.
