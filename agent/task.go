package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

// Task is a tool that runs a subagent on a self-contained question and returns its final
// answer. The subagent's searches and file dumps stay in its own history, so they cost the
// calling conversation only the answer. Its tool calls are reported, marked [task], and asked
// for as the caller's are; its usage is added to the caller's.
type Task struct {
	// New builds the subagent for one call. It must not offer Task, so subagents do not nest.
	New func() *Agent
}

// TaskPrompt is added to a subagent's system prompt.
const TaskPrompt = `# Subtask

You are a subagent. Another agent gave you the task below and sees only your final message,
not your tool calls. Make that message complete and self-contained: state what you found,
with file paths and line numbers, and say what you could not find.`

const taskDescription = `Run a subagent on a self-contained research task, such as finding where something is implemented, which files use an API, or how a module works. It has read and bash but no file-editing tools, and it knows nothing of this conversation, so give it everything it needs. It returns its final answer. Use it to keep large searches and file contents out of this conversation.`

type taskArgs struct {
	Prompt string `json:"prompt"`
}

func (Task) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "task",
		Description: taskDescription,
		Schema: map[string]any{"type": "object", "required": []string{"prompt"}, "properties": map[string]any{
			"prompt": map[string]any{"type": "string", "description": "The task, with all the context the subagent needs."},
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
	return "task " + p
}

// Paths is empty: the call itself writes nothing, and each call the subagent makes is approved
// on its own.
func (Task) Paths(json.RawMessage) ([]string, error) { return nil, nil }

func (t Task) Run(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var a taskArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return tool.Result{}, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return tool.Result{}, errors.New("prompt is required")
	}
	child := t.New()
	parent, _ := ctx.Value(runKey{}).(*runState)
	emit := func(Event) {}
	if parent != nil {
		emit = func(e Event) {
			switch e := e.(type) {
			case ToolCall:
				e.Label = "[task] " + e.Label
				parent.emit(e)
			case ToolResult:
				e.Label = "[task] " + e.Label
				parent.emit(e)
			case Retry:
				parent.emit(e)
			}
		}
	}
	res, err := child.Run(ctx, a.Prompt, emit)
	if parent != nil {
		parent.add(res.Usage)
		parent.emit(TaskDone{Usage: res.Usage, Turns: res.Turns})
	}
	summary := plural(res.Turns, "turn")
	switch {
	case err != nil && ctx.Err() != nil:
		return tool.Result{}, err
	case err != nil && res.Text != "":
		return tool.Result{Output: res.Text + "\n\n[the subagent stopped early: " + err.Error() + "]", Summary: summary, Failed: true}, nil
	case err != nil:
		return tool.Result{}, fmt.Errorf("the subagent failed: %w", err)
	}
	return tool.Result{Output: res.Text, Summary: summary}, nil
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
