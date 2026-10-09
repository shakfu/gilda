# Eval baseline: local models

Run 2026-10-09 with `scripts/eval -n 3` over the 8 tasks in `evals/`. No cloud baseline exists yet; `TODO.md` step 2 still needs one.

## Setup

- Machine: AMD Ryzen 9 7940HX, 29 GiB RAM, RTX 4060 Laptop 8 GiB (driver 595.91.07).
- llama.cpp build 11429 (`d81235049`), CUDA.
- Server: `llama-server --jinja -c 32768 -fa on -ctk q8_0 -ctv q8_0`. Models up to 5.5 GB add `-ngl 99`; larger ones omit it, so llama.cpp fits layers to free memory.
- gilda: `882dbf8` plus the uncommitted fixes of that day. See "Builds" for which run had which fix.
- Timeout: 5 minutes per run, 10 for Qwen3-Coder.

```sh
llama-server -m MODEL.gguf --alias NAME --jinja -c 32768 -fa on -ctk q8_0 -ctv q8_0
go run ./scripts/eval -m llamacpp:NAME -n 3 -timeout 5m evals/*/
```

## Results

| Model | File | Pass | Mean s/run | Mean turns | Runs that built or tested |
|-|-|-|-|-|-|
| Qwen3-Coder-30B-A3B-Instruct Q4_K_M | 18.6 GB | 23/24 | 27 | 9.1 | 12/24 |
| gemma-4-E4B-it Q5_K_M | 5.5 GB | 21/24 | 53 | 7.7 | 3/24 |
| C3SM-9B (community merge) Q5_K_M | 6.5 GB | 16/24 | 126 | 4.3 | not counted |
| LFM2.5-2.6B Q8_0 | 2.9 GB | 15/24 | 80 | 10.4 | 8/24 |
| Qwen3-4B Q8_0 | 4.3 GB | 8/24 | 116 | 4.7 | 0/24 |

"Built or tested" counts runs with a `bash` call running `go build`, `go run`, `go test`, `go vet` or `sh`.

| Task | Qwen3-Coder | C3SM-9B | gemma-4 Q5 | LFM2.5 | Qwen3-4B |
|-|-|-|-|-|-|
| `add-flag` | 3/3 | 1/3 | 2/3 | 0/3 | 0/3 |
| `add-function` | 2/3 | 2/3 | 3/3 | 0/3 | 1/3 |
| `answer-read-only` | 3/3 | 3/3 | 3/3 | 3/3 | 0/3 |
| `fix-off-by-one` | 3/3 | 2/3 | 3/3 | 3/3 | 3/3 |
| `needle-in-many-files` | 3/3 | 1/3 | 2/3 | 2/3 | 1/3 |
| `precise-edit-in-large-file` | 3/3 | 3/3 | 3/3 | 3/3 | 0/3 |
| `rename-across-files` | 3/3 | 1/3 | 2/3 | 1/3 | 0/3 |
| `shell-script` | 3/3 | 3/3 | 3/3 | 3/3 | 3/3 |

Three runs per task separate only large differences. A gap of one or two passes between two models is within noise.

## Builds

Two gilda bugs surfaced during the runs; each is in `CHANGELOG.md`.

- Malformed tool arguments were replayed verbatim, and llama-server failed every later request with HTTP 500. Fixed before the LFM2.5, C3SM-9B, Qwen3-Coder and gemma-4 Q5_K_M runs. The Qwen3-4B and gemma-4 Q4_K_M runs predate the fix; neither emitted malformed arguments.
- openai-go before v3.51.0 failed on llama-server's keep-alive comment. Only Qwen3-Coder, whose prompt processing is slow with experts on the CPU, triggered it: 3/3 on `precise-edit-in-large-file`. Its `precise-edit-in-large-file` result is a rerun on the fixed build. The gemma-4 Q5_K_M run had both fixes.

## Observations

- Qwen3-Coder passes 23/24, so the suite cannot show a gilda change that helps it. Harder tasks are needed for that, such as one long enough to need elision.
- Weaker models rarely check their work. The gemma-4 Q4_K_M and Qwen3-4B `add-flag` failures left code that does not compile or prints the wrong output; one `go build` would have caught each. The base prompt (`prompt/prompt.go`) does not ask for verification. An A/B with an appended instruction would test whether that matters.
- Qwen3-4B overflowed the 32k window on `precise-edit-in-large-file`. llama-server answered `400 ... exceeds the available context size`. The `anthropic`, `openai` and `openrouter` adapters map their providers' overflow errors to `llm.ErrContext`; `compat` maps none, so this one is not reported as context overflow.
- Quantization may matter. gemma-4 Q4_K_M (5.3 GB, 3.6 GB VRAM) passed 15/24 and Q5_K_M (5.5 GB, 4.1 GB VRAM) 21/24. Q5_K_M matched or beat Q4_K_M on every task, but a two-sided Fisher exact test gives p = 0.09, and Q4_K_M ran before both fixes. Q4_K_M was removed in favour of Q5_K_M.
- The C3SM-9B merge was removed after the run. It has no published evaluation, so its row is not reproducible.
