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

Results go to `evals/results/`, which git ignores.
