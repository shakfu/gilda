package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// fakeGilda writes a script that stands in for the binary, printing out then running then.
func fakeGilda(t *testing.T, out, then string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gilda")
	script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\n" + then + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A run killed by the timeout still reports the turns and tokens it used, its subagent's included.
func TestAKilledRunCountsItsTurns(t *testing.T) {
	out := `{"type":"start","model":"m"}
{"type":"turn","usage":{"input_tokens":100,"output_tokens":10,"cost":0.5}}
{"type":"task","turns":3,"usage":{"input_tokens":50,"output_tokens":5,"cost":0.25}}
{"type":"turn","usage":{"input_tokens":200,"output_tokens":20,"cost":null}}`
	bin := fakeGilda(t, out, "exec sleep 30")
	o := options{bin: bin, out: t.TempDir(), n: 1, timeout: time.Second}
	results, err := runAll(context.Background(), tasks(t)[:1], o, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.Outcome != "timeout" || r.Turns != 2 || r.Input != 350 || r.Output != 35 || r.Cost != 0.75 || r.Model != "m" {
		t.Fatalf("%+v", r)
	}
}

// An error before the first turn would repeat in every run, so the suite stops.
func TestASetupErrorStopsTheSuite(t *testing.T) {
	out := `{"type":"result","outcome":"error","error":"llamacpp does not offer model \"x\"","turns":0,"usage":{}}`
	bin := fakeGilda(t, out, "exit 1")
	o := options{bin: bin, out: t.TempDir(), n: 2, timeout: time.Minute}
	results, err := runAll(context.Background(), tasks(t)[:2], o, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "does not offer model") {
		t.Fatalf("err %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("%d runs after a setup error", len(results))
	}
}
