package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/mock"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/state"
)

func testModelWithSettings(t *testing.T, dir, settings string) *model {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, state.SettingsFile), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(app.Options{Mock: "../mock/say-hi.json", Root: dir, StateDir: dir, CacheDir: dir, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return newModel(context.Background(), a, Options{})
}

// held is a provider that answers once release closes.
type held struct {
	*mock.Provider
	release <-chan struct{}
}

func (h held) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	select {
	case <-h.release:
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
	return h.Provider.Stream(ctx, req, emit)
}

// withTree gives the model's conversation a task tree whose tasks answer text once release
// closes.
func withTree(t *testing.T, m *model, text string, release <-chan struct{}) *agent.Tasks {
	t.Helper()
	tasks := agent.NewTasks(agent.TaskOptions{Build: func(agent.Config, *agent.Tasks) *agent.Agent {
		return agent.New(agent.Config{Provider: held{mock.New(mock.Step{Text: text}), release}, Model: "m"})
	}})
	tasks.SetSink(m.sendBg)
	m.app.Agent.Tasks = tasks
	t.Cleanup(tasks.Close)
	return tasks
}

func startBackground(t *testing.T, tasks *agent.Tasks, name string) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"prompt": "look", "background": true, "name": name})
	if _, err := (agent.Task{Tasks: tasks}).Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
}

// nextBg waits for a message on the REPL's own channel.
func nextBg(t *testing.T, m *model) tea.Msg {
	t.Helper()
	select {
	case msg := <-m.bg:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message from the task")
	}
	return nil
}

// A task that finishes while the REPL is idle starts a turn that names it, without its answer.
func TestAFinishedTaskStartsATurnWhenIdle(t *testing.T) {
	m := testModel(t)
	defer m.tasks.wait()
	release := make(chan struct{})
	tasks := withTree(t, m, "secret answer", release)
	startBackground(t, tasks, "scan")
	m.width = 400
	if !strings.Contains(m.statusBar(), "1 task") {
		t.Fatalf("status bar %q", m.statusBar())
	}
	close(release)
	m.Update(nextBg(t, m))
	if !m.running {
		t.Fatal("the finished task started no turn")
	}
	drain(t, m)
	first := m.app.Agent.History[0]
	if !strings.Contains(first.Text, `t1 "scan" finished`) || strings.Contains(first.Text, "secret answer") {
		t.Fatalf("notice %q", first.Text)
	}
}

// With deliver = "prompt", the notice waits and goes ahead of the next prompt.
func TestDeliverPromptHoldsTheNotice(t *testing.T) {
	dir := t.TempDir()
	m := testModelWithSettings(t, dir, "[tasks]\ndeliver = \"prompt\"\n")
	defer m.tasks.wait()
	release := make(chan struct{})
	tasks := withTree(t, m, "x", release)
	startBackground(t, tasks, "scan")
	close(release)
	m.Update(nextBg(t, m))
	if m.running {
		t.Fatal("a turn started with deliver = prompt")
	}
	m.queue = append(m.queue, "what now?")
	m.Update(prepMsg{})
	drain(t, m)
	if got := m.app.Agent.History[0].Text; !strings.HasPrefix(got, "[gilda: t1") || !strings.HasSuffix(got, "what now?") {
		t.Fatalf("prompt %q", got)
	}
}

// Questions from several tasks wait their turn; each is shown once the one before is answered.
func TestApprovalsQueue(t *testing.T) {
	m := testModel(t)
	why := permission.Reason{Kind: permission.Outside, Text: "writing outside"}
	r1, r2 := make(chan bool, 1), make(chan bool, 1)
	m.Update(approvalMsg{call: llm.ToolCall{Name: "write"}, label: "write a", why: why, reply: r1})
	m.Update(approvalMsg{call: llm.ToolCall{Name: "write"}, label: "write b", why: why, reply: r2})
	if m.approval == nil || m.approval.label != "write a" || len(m.waiting) != 1 {
		t.Fatalf("approval %+v waiting %d", m.approval, len(m.waiting))
	}
	press(m, "y")
	if !<-r1 || m.approval == nil || m.approval.label != "write b" {
		t.Fatalf("the second question was not shown: %+v", m.approval)
	}
	press(m, "n")
	if <-r2 || m.approval != nil {
		t.Fatal("the second answer was lost")
	}
}

// An "a" given to the first question lets a waiting one it covers through.
func TestAlwaysAnswersAWaitingQuestion(t *testing.T) {
	m := testModel(t)
	why := permission.Reason{Kind: permission.Outside, Text: "writing outside"}
	r1, r2 := make(chan bool, 1), make(chan bool, 1)
	m.Update(approvalMsg{call: llm.ToolCall{Name: "write"}, label: "write a", why: why, reply: r1})
	m.Update(approvalMsg{call: llm.ToolCall{Name: "write"}, label: "write b", why: why, reply: r2})
	press(m, "a")
	if !<-r1 || !<-r2 || m.approval != nil {
		t.Fatalf("approval %+v", m.approval)
	}
}

func TestQuittingWithTasksRunningAsksTwice(t *testing.T) {
	m := testModel(t)
	tasks := withTree(t, m, "x", make(chan struct{}))
	startBackground(t, tasks, "slow")
	if cmd := m.quit(); cmd == nil || isQuit(cmd) {
		t.Fatal("the first quit left with a task running")
	}
	if !isQuit(m.quit()) {
		t.Fatal("the second quit stayed")
	}
}

func isQuit(cmd tea.Cmd) bool {
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestTasksCommandListsAndCancels(t *testing.T) {
	m := testModel(t)
	defer m.tasks.wait()
	tasks := withTree(t, m, "x", make(chan struct{}))
	m.command("/tasks")
	if !strings.Contains(strings.Join(m.lines, "\n"), "no background tasks") {
		t.Fatalf("lines %q", m.lines)
	}
	m.lines = nil
	startBackground(t, tasks, "slow")
	m.command("/tasks")
	if !strings.Contains(strings.Join(m.lines, "\n"), `t1 "slow" running`) {
		t.Fatalf("lines %q", m.lines)
	}
	m.lines = nil
	m.command("/tasks cancel t1")
	msg := nextBg(t, m)
	if done, ok := msg.(bgMsg).ev.(agent.TaskDone); !ok || TaskLine(done) != `[t1] "slow" cancelled` {
		t.Fatalf("after cancel: %+v", done)
	}
	// The user asked for it, so no turn starts to say so.
	m.Update(msg)
	if m.running {
		t.Fatal("a cancel the user asked for started a turn")
	}
	m.lines = nil
	m.command("/tasks cancel t9")
	if !strings.Contains(strings.Join(m.lines, "\n"), "t9: no such task") {
		t.Fatalf("lines %q", m.lines)
	}
}
