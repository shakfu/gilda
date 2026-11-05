# Evals

Scripted tasks for `scripts/eval`, which runs gilda on each and checks the result. See the
command's doc comment for the task format.

| Task | Exercises |
|-|-|
| `fix-off-by-one` | reading a failing test and fixing the code, not the test |
| `add-function` | writing new code against a hidden test |
| `rename-across-files` | a change across files and packages |
| `answer-read-only` | finding code with `read` alone, directory listings included |
| `needle-in-many-files` | searching 33 files for the one wrong value |
| `add-flag` | changing a program's behaviour without changing its default |
| `shell-script` | a non-Go task |
| `precise-edit-in-large-file` | one edit in a 1,800-line file, nothing else touched |
| `cross-package-bug` | a failing test whose cause is in another package; a hidden test catches a fix in the wrong place |
| `refactor-parse-error` | a signature change rippling through callers and tests, with the suite kept green |
| `many-chapters-elision` | reading 16 files with `read` alone in a 50k window, so old results are elided before the answer |
| `ask-when-ambiguous` | a request missing the one fact it needs; passes only if nothing changes and the answer asks |
| `keep-uncommitted-work` | a fix in a git checkout with the user's uncommitted work in the same file, which must survive |

`many-chapters-elision` was generated once with a fixed seed; its window is a guess at 70-90% of
the tokens a run reads, and differs by tokenizer. Check its `elided` column before reading its
pass rate as a measure of elision.

`scripts/baseline.sh` runs every task for each model with a key set, with the working rules on
and off.

Results go to `evals/results/`, which git ignores.
