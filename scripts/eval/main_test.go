package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func tasks(t *testing.T) []task {
	t.Helper()
	dirs, err := filepath.Glob("../../evals/*/task.toml")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no tasks: %v", err)
	}
	var out []task
	for _, d := range dirs {
		tk, err := loadTask(filepath.Dir(d))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tk)
	}
	return out
}

// A check that passes before the model has done anything measures nothing.
func TestEachCheckFailsOnTheStartingRepo(t *testing.T) {
	for _, tk := range tasks(t) {
		work := t.TempDir()
		if err := copyDir(filepath.Join(tk.Dir, "repo"), work); err != nil {
			t.Fatal(err)
		}
		_ = copyDir(filepath.Join(tk.Dir, "check"), work)
		answer := filepath.Join(t.TempDir(), "answer.txt")
		os.WriteFile(answer, nil, 0o644)
		cmd := exec.Command("sh", "-c", tk.Check)
		cmd.Dir, cmd.Env = work, append(os.Environ(), "GILDA_ANSWER="+answer)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("%s: the check passes before any change:\n%s", tk.Name, out)
		}
	}
}

// Each task's mock.json is a reference solution, so a run of every task through the built
// binary tests the harness, the tasks and their checks together.
func TestReferenceSolutionsPass(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gilda")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/gilda").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	out := t.TempDir()
	all := tasks(t)
	results, err := runAll(context.Background(), all, options{bin: bin, out: out, n: 1, timeout: 5 * time.Minute, mock: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(all) {
		t.Fatalf("%d results for %d tasks", len(results), len(all))
	}
	for _, r := range results {
		if !r.Pass || r.Outcome != "complete" || r.Turns == 0 {
			t.Errorf("%s: %+v", r.Task, r)
		}
		if _, err := os.Stat(filepath.Join(out, r.Task+"-1.jsonl")); err != nil {
			t.Errorf("%s: no run file", r.Task)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "results.json")); err != nil {
		t.Fatal("no results.json")
	}
}

func TestATaskNeedsPromptCheckAndRepo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "task.toml"), []byte("prompt = \"x\"\ncheck = \"true\"\n"), 0o644)
	if _, err := loadTask(dir); err == nil {
		t.Fatal("a task without repo/ loaded")
	}
	os.Mkdir(filepath.Join(dir, "repo"), 0o755)
	if _, err := loadTask(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "task.toml"), []byte("prompt = \"x\"\ncheck = \"true\"\nmodel = \"y\"\n"), 0o644)
	if _, err := loadTask(dir); err == nil {
		t.Fatal("an unknown key was accepted")
	}
}
