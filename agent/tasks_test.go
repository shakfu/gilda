package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/mock"
	"github.com/shakfu/gilda/tool"
)

// tree1 is a tree whose tasks cannot nest, each built by build.
func tree1(build func() *Agent) *Tasks {
	return NewTasks(TaskOptions{Depth: 1, Build: func(Config, *Tasks) *Agent { return build() }})
}

// gated holds every request until gate closes or the request is cancelled.
type gated struct {
	*mock.Provider
	gate <-chan struct{}
}

func (g gated) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
	return g.Provider.Stream(ctx, req, emit)
}

// opening calls open when request number at (from 1) starts.
type opening struct {
	*mock.Provider
	at   int
	open func()
	mu   sync.Mutex
	n    int
}

func (o *opening) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	o.mu.Lock()
	o.n++
	if o.n == o.at {
		o.open()
	}
	o.mu.Unlock()
	return o.Provider.Stream(ctx, req, emit)
}

// sink records the events a tree sends.
type sink struct {
	mu     sync.Mutex
	events []Event
}

func (s *sink) add(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *sink) all() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func bg(prompt, name string) mock.Call {
	return call("task", map[string]any{"prompt": prompt, "background": true, "name": name})
}

func withTasks(cfg Config, tasks *Tasks) *Agent {
	cfg.Tools = append(cfg.Tools, Task{Tasks: tasks}, TaskCollect{Tasks: tasks})
	a := New(cfg)
	a.Tasks = tasks
	return a
}

// The parent goes on while a background task runs; Drive tells it when the task finishes, and
// the answer comes back only through task_collect.
func TestDriveWaitsForABackgroundTaskAndTheParentCollectsIt(t *testing.T) {
	gate := make(chan struct{})
	child := gated{mock.New(mock.Step{Text: "the answer is 42", Usage: usage(10, 2, 0.01)}), gate}
	tasks := tree1(func() *Agent { return New(Config{Provider: child, Model: "m"}) })
	var s sink
	tasks.SetSink(s.add)
	parent := &opening{Provider: mock.New(
		mock.Step{Calls: []mock.Call{bg("compute", "calc")}},
		mock.Step{Text: "started it"},
		mock.Step{Calls: []mock.Call{call("task_collect", map[string]any{})}},
		mock.Step{Text: "it says 42"},
	), at: 2, open: func() { close(gate) }}
	a := withTasks(Config{Provider: parent, Model: "m"}, tasks)

	res, err := Drive(context.Background(), a, "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "it says 42" || res.Turns != 4 {
		t.Fatalf("result %+v", res)
	}
	if got := a.History[2].Results[0].Content; got != `started t1 "calc" in the background` {
		t.Fatalf("task result %q", got)
	}
	notice := a.History[4]
	if notice.Role != llm.User || !strings.Contains(notice.Text, `[gilda: t1 "calc" finished, 1 turn; read the answers with task_collect]`) {
		t.Fatalf("notice %+v", notice)
	}
	if strings.Contains(notice.Text, "42") {
		t.Fatal("the task's answer reached a user message")
	}
	if got := a.History[6].Results[0].Content; !strings.Contains(got, "t1 \"calc\" finished, 1 turn:\nthe answer is 42") {
		t.Fatalf("collect %q", got)
	}
	if u := tasks.Usage(); u.Input != 10 {
		t.Fatalf("task usage %+v", u)
	}
	var done []TaskDone
	for _, e := range s.all() {
		if d, ok := e.(TaskDone); ok {
			done = append(done, d)
		}
	}
	if len(done) != 1 || !done[0].Background || done[0].ID != "t1" || done[0].Usage.Input != 10 {
		t.Fatalf("sink TaskDone %+v", done)
	}
}

// A task that finished during a turn is named after the turn's last tool result.
func TestATaskThatFinishedIsNamedInTheNextToolResult(t *testing.T) {
	child := mock.New(mock.Step{Text: "done"})
	tasks := tree1(func() *Agent { return New(Config{Provider: child, Model: "m"}) })
	h, err := tasks.spawn(Config{}, "go", "x", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-h.done
	for len(tasks.Running()) > 0 {
		time.Sleep(time.Millisecond)
	}
	a, _, root := newAgent(t, mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "f"})}}, mock.Step{Text: "ok"})
	os.WriteFile(filepath.Join(root, "f"), []byte("x\n"), 0o644)
	a.Tasks = tasks
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if got := a.History[2].Results[0].Content; !strings.HasSuffix(got, `[gilda: t1 "x" finished, 1 turn; read the answers with task_collect]`) {
		t.Fatalf("result %q", got)
	}
	if tasks.Notice() != "" {
		t.Fatal("the task was named twice")
	}
}

// With depth 2, a task gets a scope of its own and its task gets none. The task answers only
// after its own background task has, and the answers come up the tree.
func TestNestedTasksFinishBottomUp(t *testing.T) {
	gate := make(chan struct{})
	grand := gated{mock.New(mock.Step{Text: "leaf: 7"}), gate}
	mid := &opening{Provider: mock.New(
		mock.Step{Calls: []mock.Call{bg("leaf work", "leaf")}},
		mock.Step{Text: "waiting for leaf"},
		mock.Step{Calls: []mock.Call{call("task_collect", map[string]any{})}},
		mock.Step{Text: "mid: leaf said 7"},
	), at: 2, open: func() { close(gate) }}
	var subs []*Tasks
	var mu sync.Mutex
	tasks := NewTasks(TaskOptions{Depth: 2, Build: func(_ Config, sub *Tasks) *Agent {
		mu.Lock()
		subs = append(subs, sub)
		mu.Unlock()
		if sub != nil {
			return withTasks(Config{Provider: mid, Model: "m"}, sub)
		}
		return New(Config{Provider: grand, Model: "m"})
	}})
	var s sink
	tasks.SetSink(s.add)
	parent := mock.New(
		mock.Step{Calls: []mock.Call{call("task", map[string]string{"prompt": "mid work"})}},
		mock.Step{Text: "root done"},
	)
	a := withTasks(Config{Provider: parent, Model: "m"}, tasks)
	if _, err := Drive(context.Background(), a, "go", nil); err != nil {
		t.Fatal(err)
	}
	if got := a.History[2].Results[0].Content; got != "mid: leaf said 7" {
		t.Fatalf("root's task result %q", got)
	}
	if len(subs) != 2 || subs[0] == nil || subs[1] != nil {
		t.Fatalf("scopes %v", subs)
	}
	// The leaf's events reach the sink tagged with its id; its finish is tagged with the mid task's.
	var tagged []string
	for _, e := range s.all() {
		switch e := e.(type) {
		case TaskEvent:
			if d, ok := e.Event.(TaskDone); ok {
				tagged = append(tagged, e.ID+">"+d.ID)
			}
		case TaskDone:
			t.Fatalf("untagged TaskDone %+v: a nested task's usage would count twice", e)
		}
	}
	if strings.Join(tagged, ",") != "t1>t2" {
		t.Fatalf("tagged %v", tagged)
	}
	// The mid task has finished, so its scope is closed.
	if _, err := subs[0].spawn(Config{}, "x", "", true, nil); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a finished task's scope started a task: %v", err)
	}
}

func TestATaskAtTheDepthLimitCannotStartOne(t *testing.T) {
	tasks := NewTasks(TaskOptions{Depth: 1, Build: func(Config, *Tasks) *Agent { return New(Config{Provider: mock.New(), Model: "m"}) }})
	deep := &Tasks{tree: tasks.tree, depth: 1, emit: tasks.emit}
	deep.init(context.Background())
	if _, err := deep.spawn(Config{}, "x", "", true, nil); err == nil || !strings.Contains(err.Error(), "at most 1 deep") {
		t.Fatalf("spawn at the limit: %v", err)
	}
}

func TestTheRunningCapRefusesAndNamesTheRunningTasks(t *testing.T) {
	gate := make(chan struct{})
	tasks := NewTasks(TaskOptions{Running: 1, Build: func(Config, *Tasks) *Agent {
		return New(Config{Provider: gated{mock.New(mock.Step{Text: "ok"}), gate}, Model: "m"})
	}})
	defer tasks.Close()
	if _, err := tasks.spawn(Config{}, "a", "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, err := tasks.spawn(Config{}, "b", "", true, nil)
	if err == nil || !strings.Contains(err.Error(), "1 task already running (t1)") {
		t.Fatalf("second spawn: %v", err)
	}
	close(gate)
}

// Close cancels every task, including one waiting on its provider, and waits for it.
func TestCloseCancelsRunningTasks(t *testing.T) {
	tasks := NewTasks(TaskOptions{Build: func(Config, *Tasks) *Agent {
		return New(Config{Provider: gated{mock.New(mock.Step{Text: "never"}), make(chan struct{})}, Model: "m"})
	}})
	h, err := tasks.spawn(Config{}, "a", "slow", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	tasks.Close()
	if !errors.Is(h.err, context.Canceled) || !strings.Contains(h.Status(), "cancelled") {
		t.Fatalf("after close: %v, %q", h.err, h.Status())
	}
	if _, err := tasks.spawn(Config{}, "b", "", true, nil); err == nil {
		t.Fatal("a closed scope started a task")
	}
	tasks.Reset()
	if len(tasks.Running()) != 0 || tasks.find("t1") != nil {
		t.Fatal("reset kept the old tasks")
	}
}

func TestCollectCancelsWaitsAndReportsUnknownIDs(t *testing.T) {
	gate := make(chan struct{})
	n := 0
	tasks := NewTasks(TaskOptions{Build: func(Config, *Tasks) *Agent {
		n++
		if n == 1 {
			return New(Config{Provider: gated{mock.New(mock.Step{Text: "slow"}), make(chan struct{})}, Model: "m"})
		}
		return New(Config{Provider: gated{mock.New(mock.Step{Text: "fast answer"}), gate}, Model: "m"})
	}})
	defer tasks.Close()
	tasks.spawn(Config{}, "a", "slow", true, nil)
	tasks.spawn(Config{}, "b", "fast", true, nil)
	c := TaskCollect{Tasks: tasks}

	out, err := c.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out.Output, `t1 "slow" running, 0 turns`) || !strings.Contains(out.Output, `t2 "fast" running`) {
		t.Fatalf("status %q %v", out.Output, err)
	}
	close(gate)
	out, err = c.Run(context.Background(), json.RawMessage(`{"wait":5}`))
	if err != nil || !strings.Contains(out.Output, "t2 \"fast\" finished, 1 turn:\nfast answer") || !strings.Contains(out.Output, `t1 "slow" running`) {
		t.Fatalf("wait %q %v", out.Output, err)
	}
	out, _ = c.Run(context.Background(), json.RawMessage(`{}`))
	if strings.Contains(out.Output, "fast answer") {
		t.Fatalf("an answer was printed twice: %q", out.Output)
	}
	out, _ = c.Run(context.Background(), json.RawMessage(`{"ids":["t2","t9"]}`))
	if !strings.Contains(out.Output, "fast answer") || !strings.Contains(out.Output, "t9: no such task") {
		t.Fatalf("by id %q", out.Output)
	}
	out, _ = c.Run(context.Background(), json.RawMessage(`{"cancel":["t1"],"wait":5}`))
	if !strings.Contains(out.Output, `t1 "slow" cancelled:`) || !strings.Contains(out.Output, "(cancelled before it answered)") {
		t.Fatalf("cancel %q", out.Output)
	}
}

// A wait ends at its timeout while a task still runs.
func TestCollectWaitTimesOut(t *testing.T) {
	tasks := NewTasks(TaskOptions{Build: func(Config, *Tasks) *Agent {
		return New(Config{Provider: gated{mock.New(mock.Step{Text: "x"}), make(chan struct{})}, Model: "m"})
	}})
	defer tasks.Close()
	tasks.spawn(Config{}, "a", "", true, nil)
	start := time.Now()
	out, err := TaskCollect{Tasks: tasks}.Run(context.Background(), json.RawMessage(`{"wait":0.05}`))
	if err != nil || time.Since(start) > 2*time.Second || !strings.Contains(out.Output, "running") {
		t.Fatalf("%q %v after %s", out.Output, err, time.Since(start))
	}
}

// panicky is a provider whose every request panics.
type panicky struct{ *mock.Provider }

func (panicky) Stream(context.Context, llm.Request, func(llm.Event)) (llm.Response, error) {
	panic("boom")
}

func TestAPanickingTaskFailsAlone(t *testing.T) {
	tasks := tree1(func() *Agent { return New(Config{Provider: panicky{mock.New()}, Model: "m"}) })
	h, err := tasks.spawn(Config{}, "a", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-h.done
	if h.err == nil || !strings.Contains(h.err.Error(), "panicked: boom") {
		t.Fatalf("err %v", h.err)
	}
}

// A background task's tool calls reach the sink tagged with its id, and its latest call shows in
// its status.
func TestBackgroundEventsAreTagged(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f"), []byte("x\n"), 0o644)
	gate := make(chan struct{})
	tasks := tree1(func() *Agent {
		p := mock.New(mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "f"})}}, mock.Step{Text: "done"})
		return New(Config{Provider: &opening{Provider: p, at: 2, open: func() { <-gate }}, Model: "m",
			Tools: []tool.Tool{tool.Read{Env: tool.Env{Root: root}}}})
	})
	var s sink
	tasks.SetSink(s.add)
	h, _ := tasks.spawn(Config{}, "a", "reader", true, nil)
	for !strings.Contains(h.Status(), "last: read f") {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	<-h.done
	var labels []string
	for _, e := range s.all() {
		if te, ok := e.(TaskEvent); ok {
			if r, ok := te.Event.(ToolResult); ok {
				labels = append(labels, te.ID+" "+r.Label)
			}
		}
	}
	if strings.Join(labels, "|") != "t1 read f" {
		t.Fatalf("labels %v", labels)
	}
}

func TestTaskRecordsNameTheirTask(t *testing.T) {
	rec := Record(TaskEvent{ID: "t3", Event: ToolCall{Call: llm.ToolCall{ID: "c", Name: "read", Arguments: "{}"}, Label: "read f"}})
	if rec["task"] != "t3" || rec["type"] != "tool_call" {
		t.Fatalf("record %v", rec)
	}
	rec = Record(TaskDone{ID: "t3", Background: true, Err: errors.New("x")})
	if rec["id"] != "t3" || rec["background"] != true || rec["error"] != "x" {
		t.Fatalf("record %v", rec)
	}
}

// A task stopped on request waits for the next notice instead of being news; one that finished
// by itself is news. After moves ids past those a resumed history names.
func TestStoppedTasksAreNotNewsAndIDsFollowAfter(t *testing.T) {
	never := make(chan struct{})
	n := 0
	tasks := NewTasks(TaskOptions{Build: func(Config, *Tasks) *Agent {
		n++
		if n == 1 {
			return New(Config{Provider: gated{mock.New(mock.Step{Text: "x"}), never}, Model: "m"})
		}
		return New(Config{Provider: mock.New(mock.Step{Text: "y"}), Model: "m"})
	}})
	defer tasks.Close()
	tasks.After(7)
	h, _ := tasks.spawn(Config{}, "a", "", true, nil)
	if h.ID != "t8" {
		t.Fatalf("id %s after 7", h.ID)
	}
	tasks.Cancel(h.ID)
	<-h.done
	waitFinished(t, tasks)
	if tasks.News() {
		t.Fatal("a cancelled task is news")
	}
	h2, _ := tasks.spawn(Config{}, "b", "", true, nil)
	<-h2.done
	waitFinished(t, tasks)
	if !tasks.News() {
		t.Fatal("a finished task is not news")
	}
	if n := tasks.Notice(); !strings.Contains(n, "t8") || !strings.Contains(n, "t9") {
		t.Fatalf("the notice left out a task: %q", n)
	}
}

func waitFinished(t *testing.T, tasks *Tasks) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(tasks.Running()) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("a task did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBudgetGrantsWaitsAndFails(t *testing.T) {
	b := &budget{limit: 10000, changed: make(chan struct{})}
	ctx := context.Background()
	g1, s1, err := b.reserve(ctx, 1000, 32000)
	if err != nil || g1 != 9000 {
		t.Fatalf("first grant %d %v", g1, err)
	}
	type grant struct {
		n   int64
		err error
	}
	got := make(chan grant, 1)
	go func() {
		g, s, err := b.reserve(ctx, 1000, 32000)
		if s != nil {
			defer s(&llm.Usage{Input: 1000, Output: 100})
		}
		got <- grant{g, err}
	}()
	select {
	case g := <-got:
		t.Fatalf("granted %+v while the first hold filled the budget", g)
	case <-time.After(50 * time.Millisecond):
	}
	s1(&llm.Usage{Input: 1000, Output: 500})
	s1(nil) // settles once
	if g := <-got; g.err != nil || g.n != 7500 {
		t.Fatalf("second grant %+v", g)
	}
	// 1500 + 1100 spent: a request of 7000 leaves less than the floor.
	if _, _, err := b.reserve(ctx, 7000, 32000); !errors.Is(err, ErrBudget) {
		t.Fatalf("over budget: %v", err)
	}
	// A request that did not complete is charged its whole hold.
	_, s3, _ := b.reserve(ctx, 100, 1000)
	s3(nil)
	if b.spent != 2600+1100 {
		t.Fatalf("spent %d", b.spent)
	}
	b.reset()
	if b.spent != 0 {
		t.Fatal("reset kept the spend")
	}
}

func TestBudgetWaitEndsWithItsContext(t *testing.T) {
	b := &budget{limit: 10000, changed: make(chan struct{})}
	b.reserve(context.Background(), 1000, 32000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := b.reserve(ctx, 1000, 32000); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

// A task's request goes out with the grant as its output cap; a task that finds the budget spent
// fails with ErrBudget, and the parent reads that as its answer.
func TestTasksSpendTheTreeBudget(t *testing.T) {
	first := mock.New(mock.Step{Text: "done", Usage: usage(9000, 100, 0)})
	second := mock.New(mock.Step{Text: "never"})
	n := 0
	tasks := NewTasks(TaskOptions{Budget: 20000, Build: func(Config, *Tasks) *Agent {
		n++
		if n == 1 {
			return New(Config{Provider: first, Model: "m"})
		}
		return New(Config{Provider: second, Model: "m", System: strings.Repeat("x", 4*11000)})
	}})
	h1, _ := tasks.spawn(Config{}, "a", "", true, nil)
	<-h1.done
	// The grant, the budget less the input, replaces the default cap of 32000.
	if mt := first.Requests[0].MaxTokens; mt > 20000 || mt < 19900 {
		t.Fatalf("first cap %d", mt)
	}
	if spent, limit := tasks.Budget(); spent != 9100 || limit != 20000 {
		t.Fatalf("budget %d of %d", spent, limit)
	}
	h2, _ := tasks.spawn(Config{}, "b", "", true, nil)
	<-h2.done
	if !errors.Is(h2.err, ErrBudget) || len(second.Requests) != 0 || !strings.Contains(h2.answer(), "task token budget is used up") {
		t.Fatalf("second: %v, %d requests", h2.err, len(second.Requests))
	}
	waitFinished(t, tasks)
	out, _ := tasks.report(nil)
	if last := out[len(out)-1]; last != "task budget: 9100 of 20000 tokens spent" {
		t.Fatalf("report ends %q", last)
	}
}
