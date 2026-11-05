// Command eval runs gilda on scripted tasks and checks whether each one succeeded. scripts/tally
// counts the tool calls a run makes; eval says whether the run solved its task.
//
//	go run ./scripts/eval -m openai:gpt-5.5 -n 3 evals/*
//	go run ./scripts/tally evals/results/LATEST/*.jsonl
//
// A task is a directory:
//
//	task.toml   prompt, check, and optional setup, max_turns, context and tools
//	repo/       the files the run starts from, copied to a scratch directory
//	check/      files copied in only for the check, such as hidden tests
//	mock.json   with -mock, a scripted conversation that replaces the provider
//
// setup is a shell command run in the scratch directory before gilda starts, such as one that
// commits the files to a git repository and then leaves a change uncommitted. context passes
// --context, so a task can reach elision without filling a large window. The check is a shell
// command run in the scratch directory once gilda exits; exit 0 is a pass.
// GILDA_ANSWER names a file holding the run's final text, for tasks that ask a question.
//
// The report gives, per task, the pass rate; "checked", the runs that ran bash after their last
// file change out of those that changed a file; the runs that elided old tool results; and
// failed tool calls, tokens, cost and time per run. Then it gives the tool mix, with bash split
// by the programs it ran, as scripts/tally does. A bash call after an edit counts as a check
// whatever it ran, so "checked" is an upper bound.
//
// Runs use --permissions all, so bash runs unconfined. Run untrusted tasks in a container.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/shakfu/gilda/scripts/internal/shell"
)

type task struct {
	Name     string
	Dir      string
	Prompt   string   `toml:"prompt"`
	Check    string   `toml:"check"`
	Setup    string   `toml:"setup"`
	MaxTurns int      `toml:"max_turns"`
	Context  int      `toml:"context"`
	Tools    []string `toml:"tools"`
}

type options struct {
	bin, model, settings, out string
	n                         int
	timeout                   time.Duration
	mock                      bool
}

// outcome is one run of one task.
type outcome struct {
	Task     string  `json:"task"`
	Run      int     `json:"run"`
	Pass     bool    `json:"pass"`
	Outcome  string  `json:"outcome"`
	Error    string  `json:"error,omitempty"`
	Turns    int     `json:"turns"`
	Input    int64   `json:"input_tokens"`
	Output   int64   `json:"output_tokens"`
	Cost     float64 `json:"cost"`
	Seconds  float64 `json:"seconds"`
	Model    string  `json:"model"`
	CheckOut string  `json:"check_output,omitempty"`
	// Tools counts tool calls by scripts/internal/shell.ToolName, the subagent's included.
	Tools  map[string]int `json:"tools,omitempty"`
	Failed int            `json:"failed_calls"`
	// Edited is set when the run changed a file through a tool, and Checked when a bash call
	// followed its last change, as a build or test would. The subagent is not counted: it
	// cannot edit.
	Edited  bool `json:"edited"`
	Checked bool `json:"checked"`
	// Elided counts the times old tool results were elided.
	Elided int `json:"elided"`
	// recorded is set when gilda wrote a result record, so the outcome is its own.
	recorded bool
}

func main() {
	var o options
	flag.StringVar(&o.bin, "bin", "bin/gilda", "the gilda binary")
	flag.StringVar(&o.model, "m", "", "model, or PROVIDER:MODEL, passed to gilda -m")
	flag.StringVar(&o.settings, "settings", "", "a settings.toml for every run, for A/B comparisons")
	flag.StringVar(&o.out, "out", "", "directory for each run's JSON lines and results.json (default: evals/results/TIME)")
	flag.IntVar(&o.n, "n", 1, "runs per task")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Minute, "bound on one run and on its check")
	flag.BoolVar(&o.mock, "mock", false, "replay each task's mock.json instead of calling a provider")
	flag.Parse()
	if o.out == "" {
		o.out = filepath.Join("evals", "results", time.Now().Format("20060102-150405"))
	}
	dirs := flag.Args()
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: eval [flags] TASK_DIR ...")
		os.Exit(2)
	}
	var tasks []task
	for _, d := range dirs {
		t, err := loadTask(d)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		tasks = append(tasks, t)
	}
	results, err := runAll(context.Background(), tasks, o, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report(os.Stdout, results)
	fmt.Fprintln(os.Stderr, "runs saved in", o.out)
}

func loadTask(dir string) (task, error) {
	t := task{Name: filepath.Base(filepath.Clean(dir)), Dir: dir}
	meta, err := toml.DecodeFile(filepath.Join(dir, "task.toml"), &t)
	if err != nil {
		return t, fmt.Errorf("%s: %w", dir, err)
	}
	if extra := meta.Undecoded(); len(extra) > 0 {
		return t, fmt.Errorf("%s/task.toml: unknown key %s", dir, extra[0])
	}
	if t.Prompt == "" || t.Check == "" {
		return t, fmt.Errorf("%s/task.toml: prompt and check are required", dir)
	}
	if info, err := os.Stat(filepath.Join(dir, "repo")); err != nil || !info.IsDir() {
		return t, fmt.Errorf("%s: no repo directory", dir)
	}
	return t, nil
}

func runAll(ctx context.Context, tasks []task, o options, progress io.Writer) ([]outcome, error) {
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return nil, err
	}
	var results []outcome
	for _, t := range tasks {
		for i := 1; i <= o.n; i++ {
			r, err := runOnce(ctx, t, i, o)
			if err != nil {
				return results, fmt.Errorf("%s run %d: %w", t.Name, i, err)
			}
			// An error before the first turn, such as an unknown model or a missing key, would
			// repeat in every run.
			if len(results) == 0 && r.recorded && r.Outcome == "error" && r.Turns == 0 {
				return results, fmt.Errorf("%s run %d failed before its first turn: %s", t.Name, i, r.Error)
			}
			mark := "FAIL"
			if r.Pass {
				mark = "pass"
			}
			fmt.Fprintf(progress, "%s %s #%d: %s, %d turns, %.0fs\n", mark, t.Name, i, r.Outcome, r.Turns, r.Seconds)
			results = append(results, r)
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return results, err
	}
	return results, os.WriteFile(filepath.Join(o.out, "results.json"), data, 0o644)
}

// runOnce copies the task's repo to a scratch directory, runs gilda on it with state of its
// own, then copies in the check files and runs the check.
func runOnce(ctx context.Context, t task, i int, o options) (outcome, error) {
	scratch, err := os.MkdirTemp("", "gilda-eval-")
	if err != nil {
		return outcome{}, err
	}
	defer os.RemoveAll(scratch)
	work, home := filepath.Join(scratch, "work"), filepath.Join(scratch, "home")
	if err := copyDir(filepath.Join(t.Dir, "repo"), work); err != nil {
		return outcome{}, err
	}
	if o.settings != "" {
		if err := copyFile(o.settings, filepath.Join(home, "config", "gilda", "settings.toml")); err != nil {
			return outcome{}, err
		}
	}
	args := []string{"-p", t.Prompt, "--json", "--permissions", "all", "-C", work}
	if o.model != "" {
		args = append(args, "-m", o.model)
	}
	if o.mock {
		mock, err := filepath.Abs(filepath.Join(t.Dir, "mock.json"))
		if err != nil {
			return outcome{}, err
		}
		args = append(args, "--mock", mock)
	}
	if t.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprint(t.MaxTurns))
	}
	if t.Context > 0 {
		args = append(args, "--context", fmt.Sprint(t.Context))
	}
	if len(t.Tools) > 0 {
		args = append(args, "--tools", strings.Join(t.Tools, ","))
	}
	if err := setup(ctx, t, work, home); err != nil {
		return outcome{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	cmd := exec.CommandContext(rctx, o.bin, args...)
	cmd.Env = env(home)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	start := time.Now()
	runErr := cmd.Run()
	r := outcome{Task: t.Name, Run: i, Seconds: time.Since(start).Seconds()}
	jsonl := filepath.Join(o.out, fmt.Sprintf("%s-%d.jsonl", t.Name, i))
	if err := os.WriteFile(jsonl, []byte(stdout.String()), 0o644); err != nil {
		return r, err
	}
	text := r.read(stdout.String())
	switch {
	case r.recorded:
	case errors.Is(rctx.Err(), context.DeadlineExceeded):
		r.Outcome, r.Error = "timeout", fmt.Sprintf("killed after %s", o.timeout)
	default:
		r.Outcome, r.Error = "error", fmt.Sprintf("no result record: %v", runErr)
	}

	if err := copyDir(filepath.Join(t.Dir, "check"), work); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return r, err
	}
	answer := filepath.Join(scratch, "answer.txt")
	if err := os.WriteFile(answer, []byte(text), 0o644); err != nil {
		return r, err
	}
	cctx, ccancel := context.WithTimeout(ctx, o.timeout)
	defer ccancel()
	check := exec.CommandContext(cctx, "sh", "-c", t.Check)
	check.Dir = work
	check.Env = append(env(home), "GILDA_ANSWER="+answer)
	out, err := check.CombinedOutput()
	r.Pass = err == nil
	if !r.Pass {
		r.CheckOut = tail(string(out), 2000)
	}
	return r, nil
}

// read fills r from the run's result record and returns its final text. A run killed before
// its result record is counted from its turn and task records; a subagent killed mid-run is lost.
func (r *outcome) read(jsonl string) string {
	var text string
	var partial outcome
	sc := bufio.NewScanner(strings.NewReader(jsonl))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			Label   string `json:"label"`
			OK      bool   `json:"ok"`
			Outcome string `json:"outcome"`
			Error   string `json:"error"`
			Text    string `json:"text"`
			Turns   int    `json:"turns"`
			Model   string `json:"model"`
			Usage   struct {
				Input  int64    `json:"input_tokens"`
				Output int64    `json:"output_tokens"`
				Cost   *float64 `json:"cost"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		var cost float64
		if rec.Usage.Cost != nil {
			cost = *rec.Usage.Cost
		}
		switch rec.Type {
		case "tool_result":
			r.tool(rec.Name, rec.Label, rec.OK)
		case "elided":
			r.Elided++
		case "start":
			partial.Model = rec.Model
		case "turn", "task":
			if rec.Type == "turn" {
				partial.Turns++
			}
			partial.Input += rec.Usage.Input
			partial.Output += rec.Usage.Output
			partial.Cost += cost
		case "result":
			r.Outcome, r.Error, r.Turns, r.Model = rec.Outcome, rec.Error, rec.Turns, rec.Model
			r.Input, r.Output, r.Cost = rec.Usage.Input, rec.Usage.Output, cost
			r.recorded, text = true, rec.Text
		}
	}
	if !r.recorded {
		r.Turns, r.Input, r.Output, r.Cost, r.Model = partial.Turns, partial.Input, partial.Output, partial.Cost, partial.Model
	}
	return text
}

// setup runs the task's setup command in work. A failure is the task's fault, not the run's,
// so it stops the suite.
func setup(ctx context.Context, t task, work, home string) error {
	if t.Setup == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", t.Setup)
	cmd.Dir, cmd.Env = work, env(home)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("setup: %v\n%s", err, out)
	}
	return nil
}

// changers are the tools that change files.
var changers = map[string]bool{"write": true, "edit": true, "apply_patch": true}

// tool counts one tool_result record.
func (r *outcome) tool(name, label string, ok bool) {
	if r.Tools == nil {
		r.Tools = map[string]int{}
	}
	r.Tools[shell.ToolName(name, label)]++
	if !ok {
		r.Failed++
	}
	if strings.HasPrefix(label, "[task] ") {
		return
	}
	switch {
	case changers[name] && ok:
		r.Edited, r.Checked = true, false
	case name == "bash" && r.Edited:
		r.Checked = true
	}
}

// env is the process environment without GILDA_ variables, which would change what a run does,
// and with XDG directories under home, so runs share no state, trust or settings.
func env(home string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GILDA_") && !strings.HasPrefix(kv, "XDG_") {
			out = append(out, kv)
		}
	}
	return append(out,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"))
}

// report prints a row per task, then the tool mix of all runs.
func report(w io.Writer, results []outcome) {
	type agg struct {
		runs, passes, turns, failed, edited, checked, elided int
		in, out                                              int64
		cost, secs                                           float64
	}
	byTask := map[string]*agg{}
	var names []string
	total := &agg{}
	tools := map[string]int{}
	for _, r := range results {
		a := byTask[r.Task]
		if a == nil {
			a = &agg{}
			byTask[r.Task] = a
			names = append(names, r.Task)
		}
		for _, x := range []*agg{a, total} {
			x.runs++
			if r.Pass {
				x.passes++
			}
			if r.Edited {
				x.edited++
				if r.Checked {
					x.checked++
				}
			}
			x.turns += r.Turns
			x.failed += r.Failed
			if r.Elided > 0 {
				x.elided++
			}
			x.in += r.Input
			x.out += r.Output
			x.cost += r.Cost
			x.secs += r.Seconds
		}
		for t, n := range r.Tools {
			tools[t] += n
		}
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "task\tpass\tchecked\telided\tturns\tfailed calls\tin\tout\tcost\tseconds\t")
	row := func(name string, a *agg) {
		n := float64(a.runs)
		fmt.Fprintf(tw, "%s\t%d/%d\t%d/%d\t%d\t%.1f\t%.1f\t%.0f\t%.0f\t$%.4f\t%.0f\t\n", name, a.passes, a.runs,
			a.checked, a.edited, a.elided, float64(a.turns)/n, float64(a.failed)/n, float64(a.in)/n, float64(a.out)/n, a.cost, a.secs/n)
	}
	for _, name := range names {
		row(name, byTask[name])
	}
	if len(names) > 1 {
		row("all", total)
	}
	tw.Flush()
	if len(tools) == 0 {
		return
	}
	keys := make([]string, 0, len(tools))
	for t := range tools {
		keys = append(keys, t)
	}
	sort.Slice(keys, func(i, j int) bool {
		if tools[keys[i]] != tools[keys[j]] {
			return tools[keys[i]] > tools[keys[j]]
		}
		return keys[i] < keys[j]
	})
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "tool\tcalls\tper run\t")
	for _, t := range keys {
		fmt.Fprintf(tw, "%s\t%d\t%.1f\t\n", t, tools[t], float64(tools[t])/float64(len(results)))
	}
	tw.Flush()
}

func copyDir(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

// tail keeps the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
