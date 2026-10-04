# Design decisions

Recorded 2026-09-23 for 0.1.0.

## History is neutral, with native payloads attached

`llm.Message` holds text, tool calls and tool results. An assistant message also carries `Native`: the provider's own representation, tagged with provider and model.

| Provider | Native payload | Why it matters |
|-|-|-|
| anthropic | `MessageParam` from the accumulated `Message` | thinking blocks and their signatures must replay unchanged |
| openai | the response's output items as input items | with `store=false`, encrypted reasoning survives the tool calls of a turn only if the client sends it back |
| openrouter | merged `reasoning_details` | the same, through OpenRouter |
| compat | none | local servers keep no replayable reasoning |

A provider replays `Native` only for the model that produced it. Otherwise it rebuilds the message from the neutral fields and the reasoning is dropped.

Chosen over one history shape per provider, which would make a mid-session switch impossible. It was also chosen over a neutral-only history, which myra's `docs/dev/native-providers.md` identifies as the place translation loses data. The cost is one `any` field and a type assertion per adapter.

Open question: Claude Fable 5.1 and Opus 5.5 reject edited history for accounts created on or after 2026-08-31. A switch away from such a model and back replays its thinking blocks, but the turns in between carry none. Whether the API treats that as an edit is untested.

## Streaming merges OpenRouter reasoning fragments

OpenRouter streams `reasoning_details` as fragments sharing a type and an index. Replaying them as received would send one entry per token. `mergeReasoning` joins consecutive fragments and keeps the signature. That replay is accepted upstream is unverified: no key was available when this was written.

## Binary size

Stripped sizes of minimal programs, 2026-09-23:

| Program | MB |
|-|-|
| `net/http` only | 3.2 |
| + anthropic-sdk-go streaming | 10.1 |
| + openai-go Responses and Chat | 9.0 |
| + OpenRouter SDK chat | 7.4 |
| Bubble Tea v2 + textarea | 2.8 |

gilda is 40 MB stripped. Startup is 10 ms. A build tag per provider would cut a build to the SDKs it uses, if size becomes a constraint.

## Bubble Tea v2 and the inline renderer

The REPL runs inline, so finished output stays in terminal scrollback. Bubble Tea v2.0.9 with ultraviolet `v0.0.0-20260811164956` left stale rows when an inline frame shrank. On a 10-to-3-row shrink it emitted `ESC[2A` then `ESC[J`, so it moved up by the new height rather than the old. Ultraviolet `v0.0.0-20260922123528-4e49372c11f9` fixes it, and `go.mod` pins that commit. Revert the pin to a tagged release once one includes the fix.

Each update prints through `tea.Sequence(print, listen)`. v2 runs commands in separate goroutines, so separate `tea.Println` calls could reorder streamed lines.

## What was left out

- A sandbox. `--permissions` (the `permission` package) catches misdirected writes but does not confine `bash`. `docs/dev/permissions.md` has a design, with links to minima's Linux and macOS implementations.

- Summarising compaction. Old tool results are elided instead (`agent/elide.go`).

- A `thinking` parameter for Anthropic. gilda omits it, so each model runs its default: adaptive on Opus 5, Opus 5.5, Fable and Sonnet 5, none on Opus 4.8 and 4.7.

- OpenAI `include: reasoning.encrypted_content` is sent for every model. It is untested whether a non-reasoning model rejects it.
