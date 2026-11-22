package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

// Task is a tool that runs a subagent on a self-contained question. Synchronously, it returns
// the subagent's final answer, so the subagent's searches and file dumps cost the calling
// conversation only the answer. In the background, it returns at once and TaskCollect reads the
// answer later. Tool calls are reported and asked for as the caller's are; usage is added to the
// caller's.
type Task struct {
	Tasks *Tasks
}

// TaskPrompt is added to a subagent's system prompt.
const TaskPrompt = `# Subtask

You are a subagent. Another agent gave you the task below and sees only your final message,
not your tool calls. Make that message complete and self-contained: state what you found,
with file paths and line numbers, and say what you could not find.`

const taskDescription = `Run a subagent on a self-contained research task, such as finding where something is implemented, which files use an API, or how a module works. It has read and bash but no file-editing tools, and it knows nothing of this conversation, so give it everything it needs. It returns its final answer. Use it to keep large searches and file contents out of this conversation.

With background true, it returns at once with a task id and the subagent runs while you work. Use it when the user asks for parallel or delegated work, or for long independent investigations; each subagent rebuilds the context you already have, so for a quick lookup work directly. You are told when one finishes; read its answer with task_collect. Collect every answer you need before giving your own.`

type taskArgs struct {
	Prompt     string `json:"prompt"`
	Background bool   `json:"background"`
	Name       string `json:"name"`
}

func (Task) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "task",
		Description: taskDescription,
		Schema: map[string]any{"type": "object", "required": []string{"prompt"}, "properties": map[string]any{
			"prompt":     map[string]any{"type": "string", "description": "The task, with all the context the subagent needs."},
			"background": map[string]any{"type": "boolean", "description": "Return at once and run the subagent in the background."},
			"name":       map[string]any{"type": "string", "description": "A short name for a background task, such as auth-callers."},
		}},
	}
}

func (Task) Label(raw json.RawMessage) string {
	var a taskArgs
	_ = json.Unmarshal(raw, &a)
	p := strings.Join(strings.Fields(a.Prompt), " ")
	if r := []rune(p); len(r) > 60 {
		p = string(r[:57]) + "..."
	}
	if a.Background {
		return "task & " + p
	}
	return "task " + p
}

// Paths is empty: the call itself writes nothing, and each call the subagent makes is approved
// on its own.
func (Task) Paths(json.RawMessage) ([]string, error) { return nil, nil }

// maxName bounds a task's name, which every line about the task repeats.
const maxName = 32

func (t Task) Run(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var a taskArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return tool.Result{}, errors.New("prompt is required")
	}
	name := strings.Join(strings.Fields(a.Name), "-")
	if r := []rune(name); len(r) > maxName {
		name = string(r[:maxName])
	}
	var cfg Config
	emit := func(Event) {}
	parent, _ := ctx.Value(runKey{}).(*runState)
	if parent != nil {
		cfg, emit = parent.agent.Config, parent.emit
	}
	h, err := t.Tasks.spawn(cfg, a.Prompt, name, a.Background, emit)
	if err != nil {
		return tool.Result{}, err
	}
	if a.Background {
		return tool.Result{Output: fmt.Sprintf("started %s %q in the background", h.ID, h.Name), Summary: "started " + h.ID}, nil
	}

	select {
	case <-h.done:
	case <-ctx.Done():
		h.stop()
		<-h.done
	}
	res, err := h.res, h.err
	if parent != nil {
		parent.add(res.Usage)
		parent.emit(TaskDone{ID: h.ID, Name: h.Name, Usage: res.Usage, Turns: res.Turns, Err: err})
	}
	summary := plural(res.Turns, "turn")
	switch {
	case err != nil && ctx.Err() != nil:
		return tool.Result{}, ctx.Err()
	case err != nil && res.Text != "":
		return tool.Result{Output: res.Text + "\n\n[the subagent stopped early: " + err.Error() + "]", Summary: summary, Failed: true}, nil
	case err != nil:
		return tool.Result{}, fmt.Errorf("the subagent failed: %w", err)
	}
	return tool.Result{Output: res.Text, Summary: summary}, nil
}

// TaskCollect is the tool that reads, waits for and cancels background tasks.
type TaskCollect struct {
	Tasks *Tasks
}

const collectDescription = `Read the answers of background tasks started with task. It prints each finished answer not read yet, then a status line for each task still running. With ids, it prints those tasks, answers included even if read before. With wait, it first waits up to that many seconds for one of them to finish. cancel stops those tasks first.`

// maxWait bounds task_collect's wait, as bash_max_timeout bounds a command.
const maxWait = 600

type collectArgs struct {
	IDs    []string `json:"ids"`
	Wait   float64  `json:"wait"`
	Cancel []string `json:"cancel"`
}

func (TaskCollect) Spec() llm.ToolSpec {
	ids := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return llm.ToolSpec{
		Name:        "task_collect",
		Description: collectDescription,
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"ids":    withDescription(ids, "Task ids, such as t1; empty means every task."),
			"wait":   map[string]any{"type": "number", "description": fmt.Sprintf("Seconds to wait for a task to finish, at most %d.", maxWait)},
			"cancel": withDescription(ids, "Task ids to stop."),
		}},
	}
}

func withDescription(schema map[string]any, d string) map[string]any {
	out := map[string]any{"description": d}
	for k, v := range schema {
		out[k] = v
	}
	return out
}

func (TaskCollect) Label(raw json.RawMessage) string {
	var a collectArgs
	_ = json.Unmarshal(raw, &a)
	l := "task_collect"
	if len(a.IDs) > 0 {
		l += " " + strings.Join(a.IDs, ",")
	}
	if a.Wait > 0 {
		l += fmt.Sprintf(" wait %gs", a.Wait)
	}
	if len(a.Cancel) > 0 {
		l += " cancel " + strings.Join(a.Cancel, ",")
	}
	return l
}

// ReadOnly holds: collecting starts no process and writes nothing.
func (TaskCollect) ReadOnly() bool { return true }

func (c TaskCollect) Run(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var a collectArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	var lines []string
	for _, id := range a.Cancel {
		if err := c.Tasks.Cancel(id); err != nil {
			lines = append(lines, err.Error())
		}
	}
	if a.Wait > 0 {
		if err := c.Tasks.waitFor(ctx, a.IDs, time.Duration(min(a.Wait, maxWait)*float64(time.Second))); err != nil {
			return tool.Result{}, err
		}
	}
	out, summary := c.Tasks.report(a.IDs)
	return tool.Result{Output: strings.Join(append(lines, out...), "\n\n"), Summary: summary}, nil
}

// targets is the tasks ids names, or every task; unknown ids are returned apart.
func (t *Tasks) targets(ids []string) (hs []*Handle, unknown []string) {
	if len(ids) == 0 {
		return t.list, nil
	}
	for _, id := range ids {
		found := false
		for _, h := range t.list {
			if h.ID == id {
				hs, found = append(hs, h), true
				break
			}
		}
		if !found {
			unknown = append(unknown, id)
		}
	}
	return hs, unknown
}

// waitFor returns once a task among ids finishes unread, none of them is running, or d passes.
func (t *Tasks) waitFor(ctx context.Context, ids []string, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		if t.ready(ids) {
			return nil
		}
		select {
		case <-t.wake:
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *Tasks) ready(ids []string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	hs, _ := t.targets(ids)
	running := false
	for _, h := range hs {
		if h.finished && !h.collected {
			return true
		}
		running = running || !h.finished
	}
	return !running
}

// report prints the answers and status lines task_collect returns, and marks the answers read.
func (t *Tasks) report(ids []string) (out []string, summary string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.list) == 0 {
		return []string{"no background tasks have been started"}, "none"
	}
	hs, unknown := t.targets(ids)
	var answers, running []string
	for _, h := range hs {
		switch {
		case !h.finished:
			running = append(running, h.Status())
		case len(ids) > 0 || !h.collected:
			h.collected, h.notified = true, true
			answers = append(answers, h.Status()+":\n"+h.answer())
		}
	}
	for _, id := range unknown {
		answers = append(answers, id+": "+noTask)
	}
	out = append(answers, running...)
	if len(out) == 0 {
		out = []string{"no answers to read: every finished task was read"}
	}
	if b := t.tree.budget; b != nil {
		b.mu.Lock()
		out = append(out, fmt.Sprintf("task budget: %d of %d tokens spent", b.spent, b.limit))
		b.mu.Unlock()
	}
	return out, fmt.Sprintf("%s, %d running", plural(len(answers), "answer"), len(running))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// runKey carries a run's state to the tools it calls, so Task can report through the run.
type runKey struct{}

type runState struct {
	agent *Agent
	res   *Result
	emit  func(Event)
}

// add counts a subagent's usage in the run and the session.
func (s *runState) add(u llm.Usage) {
	s.res.Usage.Add(u)
	s.agent.Usage.Add(u)
}
