package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shakfu/gilda/llm"
)

// Tasks holds the subagents one agent started. Every scope in a tree shares its limits, its
// task ids and its sink. Cancelling a scope stops every task under it. See
// docs/dev/async-tasks.md.
type Tasks struct {
	tree  *tree
	depth int // the owning agent's depth; the root is 0
	// emit reports this scope's finished tasks: straight to the sink for the root, wrapped in
	// the owner's TaskEvent below it.
	emit func(Event)

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	list   []*Handle // background tasks, in start order
	usage  llm.Usage // finished background tasks, across Reset
	wake   chan struct{}
	wg     sync.WaitGroup
}

// TaskOptions configures a tree of tasks.
type TaskOptions struct {
	// Depth is how deep tasks nest: 1 lets the root start tasks that cannot start their own.
	// Default 2.
	Depth int
	// Running bounds the tasks running at once across the tree. Default 4.
	Running int
	// Budget bounds the tokens, input and output, that all tasks of the tree may use until
	// Reset; 0 means no bound. The conversation that starts them is not counted.
	Budget int64
	// Build makes the agent for one task from the config of the agent starting it. sub is the
	// task's own scope, nil when it may not start tasks; Build offers it the task tools.
	Build func(parent Config, sub *Tasks) *Agent
}

type tree struct {
	depth, running int
	build          func(Config, *Tasks) *Agent
	budget         *budget // nil without one

	mu   sync.Mutex
	seq  int
	live map[string]*Handle // every running task, synchronous ones included
	sink func(Event)
}

// NewTasks makes the root scope of a tree.
func NewTasks(opts TaskOptions) *Tasks {
	if opts.Depth <= 0 {
		opts.Depth = 2
	}
	if opts.Running <= 0 {
		opts.Running = 4
	}
	tr := &tree{depth: opts.Depth, running: opts.Running, build: opts.Build, live: map[string]*Handle{}}
	if opts.Budget > 0 {
		tr.budget = &budget{limit: opts.Budget, changed: make(chan struct{})}
	}
	t := &Tasks{tree: tr, emit: tr.send}
	t.init(context.Background())
	return t
}

func (t *Tasks) init(parent context.Context) {
	t.ctx, t.cancel = context.WithCancel(parent)
	t.list, t.wake = nil, make(chan struct{}, 1)
}

// SetSink sets where events from background tasks go; nil drops them. Tasks call it from their
// own goroutines, so it must be safe for concurrent use.
func (t *Tasks) SetSink(sink func(Event)) {
	if t == nil {
		return
	}
	t.tree.mu.Lock()
	t.tree.sink = sink
	t.tree.mu.Unlock()
}

func (tr *tree) send(e Event) {
	tr.mu.Lock()
	sink := tr.sink
	tr.mu.Unlock()
	if sink != nil {
		sink(e)
	}
}

// Close cancels every task in the scope and waits for them to return.
func (t *Tasks) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	cancel()
	t.wg.Wait()
}

// Reset cancels every task, waits for them, and forgets them, as a new conversation needs.
// Usage is kept, as the session's is.
func (t *Tasks) Reset() {
	if t == nil {
		return
	}
	t.Close()
	t.mu.Lock()
	t.init(context.Background())
	t.mu.Unlock()
	t.tree.budget.reset()
}

// Budget reports the tokens tasks spent and the bound; limit is 0 without a budget.
func (t *Tasks) Budget() (spent, limit int64) {
	if t == nil || t.tree.budget == nil {
		return 0, 0
	}
	b := t.tree.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent, b.limit
}

// Usage totals the background tasks that have finished.
func (t *Tasks) Usage() llm.Usage {
	if t == nil {
		return llm.Usage{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usage
}

// Handle is one task.
type Handle struct {
	ID, Name   string
	Background bool
	done       chan struct{}
	stop       context.CancelFunc
	last       atomic.Pointer[string] // the label of its latest tool call
	turns      atomic.Int64

	// Set before done is closed.
	res Result
	err error
	// Guarded by the scope's mu. stopped is set when Cancel asked for the task to end.
	finished, collected, notified, stopped bool
}

// List reports the background tasks of this scope, in start order.
func (t *Tasks) List() []*Handle {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*Handle(nil), t.list...)
}

// Running reports the tasks of this scope still running.
func (t *Tasks) Running() []*Handle {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*Handle
	for _, h := range t.list {
		if !h.finished {
			out = append(out, h)
		}
	}
	return out
}

// Status is one line describing a task: its id, name, state and, while it runs, its latest
// tool call.
func (h *Handle) Status() string {
	select {
	case <-h.done:
		return fmt.Sprintf("%s %q %s", h.ID, h.Name, outcome(h.res, h.err))
	default:
	}
	s := fmt.Sprintf("%s %q running, %s", h.ID, h.Name, plural(int(h.turns.Load()), "turn"))
	if l := h.last.Load(); l != nil {
		s += ", last: " + *l
	}
	return s
}

func outcome(res Result, err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case err != nil:
		return "failed"
	}
	return "finished, " + plural(res.Turns, "turn")
}

// answer is what task_collect prints for a finished task.
func (h *Handle) answer() string {
	text := strings.TrimSpace(h.res.Text)
	switch {
	case h.err != nil && text != "":
		return text + "\n\n[the task stopped early: " + h.err.Error() + "]"
	case errors.Is(h.err, context.Canceled):
		return "(cancelled before it answered)"
	case h.err != nil:
		return "(failed: " + h.err.Error() + ")"
	case text == "":
		return "(no answer)"
	}
	return text
}

// spawn starts a task under t. emit receives a synchronous task's events; a background task's
// go to the sink, wrapped in TaskEvent.
func (t *Tasks) spawn(parent Config, prompt, name string, background bool, emit func(Event)) (*Handle, error) {
	tr := t.tree
	if t.depth >= tr.depth {
		return nil, fmt.Errorf("tasks nest at most %d deep", tr.depth)
	}
	t.mu.Lock()
	ctx := t.ctx
	t.mu.Unlock()
	if ctx.Err() != nil {
		return nil, errors.New("the tasks were cancelled")
	}

	tr.mu.Lock()
	if len(tr.live) >= tr.running {
		var ids []string
		for id := range tr.live {
			ids = append(ids, id)
		}
		tr.mu.Unlock()
		return nil, fmt.Errorf("%s already running (%s), the most allowed; collect or cancel one first",
			plural(len(ids), "task"), strings.Join(sorted(ids), ", "))
	}
	tr.seq++
	h := &Handle{ID: fmt.Sprintf("t%d", tr.seq), Name: name, Background: background, done: make(chan struct{})}
	if h.Name == "" {
		h.Name = h.ID
	}
	tr.live[h.ID] = h
	tr.mu.Unlock()

	tctx, stop := context.WithCancel(ctx)
	h.stop = stop
	tagged := func(e Event) { tr.send(TaskEvent{ID: h.ID, Name: h.Name, Event: e}) }
	var sub *Tasks
	if t.depth+1 < tr.depth {
		sub = &Tasks{tree: tr, depth: t.depth + 1, emit: tagged}
		sub.init(tctx)
	}
	child := tr.build(parent, sub)
	child.Tasks = sub
	if tr.budget != nil {
		child.Reserve = tr.budget.reserve
	}

	forward := func(e Event) {
		switch e := e.(type) {
		case ToolCall:
			h.last.Store(&e.Label)
		case Response:
			h.turns.Add(1)
		}
		switch e := e.(type) {
		case ToolCall, ToolResult, Retry:
			if background {
				tagged(e)
			} else {
				emit(prefixed(e))
			}
		case TaskDone:
			// A finished task's usage is already in its parent's total, so it is shown, not counted.
			if background {
				tagged(e)
			}
		}
	}

	if background {
		t.mu.Lock()
		t.list = append(t.list, h)
		t.mu.Unlock()
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer stop()
		res, err := run(tctx, child, prompt, forward)
		if sub != nil {
			sub.Close()
			res.Usage.Add(sub.Usage())
		}
		h.res, h.err = res, err

		tr.mu.Lock()
		delete(tr.live, h.ID)
		tr.mu.Unlock()
		// done closes first, so a handle marked finished always reports so in Status.
		close(h.done)
		t.mu.Lock()
		h.finished = true
		if background {
			t.usage.Add(res.Usage)
		}
		t.mu.Unlock()
		select {
		case t.wake <- struct{}{}:
		default:
		}
		if background {
			t.emit(TaskDone{ID: h.ID, Name: h.Name, Background: true, Usage: res.Usage, Turns: res.Turns, Err: err})
		}
	}()
	return h, nil
}

// run drives a task's agent, turning a panic into its error so one task cannot end the process.
func run(ctx context.Context, a *Agent, prompt string, emit func(Event)) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the task panicked: %v", r)
		}
	}()
	return Drive(ctx, a, prompt, emit)
}

// prefixed marks a synchronous task's event for its caller's frontend.
func prefixed(e Event) Event {
	switch e := e.(type) {
	case ToolCall:
		e.Label = "[task] " + e.Label
		return e
	case ToolResult:
		e.Label = "[task] " + e.Label
		return e
	}
	return e
}

// Cancel stops the task with this id and everything under it. Stopping a finished task does
// nothing.
func (t *Tasks) Cancel(id string) error {
	h := t.find(id)
	if h == nil {
		return fmt.Errorf("%s: %s", id, noTask)
	}
	t.mu.Lock()
	h.stopped = !h.finished
	t.mu.Unlock()
	h.stop()
	return nil
}

// noTask explains an unknown id: ids from before a resume name tasks that ended with gilda.
const noTask = "no such task in this run; tasks are not saved, so they end when gilda does"

// News reports whether a task finished that nobody has heard of and nobody asked to stop, so
// a frontend may start a turn to tell the agent. A stopped task waits for the next notice.
func (t *Tasks) News() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, h := range t.list {
		if h.finished && !h.notified && !h.collected && !h.stopped {
			return true
		}
	}
	return false
}

// After makes new task ids follow n, so a resumed conversation that names t1 to tn does not
// get a second t1.
func (t *Tasks) After(n int) {
	if t == nil {
		return
	}
	t.tree.mu.Lock()
	t.tree.seq = max(t.tree.seq, n)
	t.tree.mu.Unlock()
}

func (t *Tasks) find(id string) *Handle {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, h := range t.list {
		if h.ID == id {
			return h
		}
	}
	return nil
}

// Notice names the tasks that finished since the last notice or collection, for the agent's
// next message; empty when there are none. Those tasks are not named again.
func (t *Tasks) Notice() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var done []string
	running := 0
	for _, h := range t.list {
		switch {
		case !h.finished:
			running++
		case !h.notified && !h.collected:
			h.notified = true
			done = append(done, fmt.Sprintf("%s %q %s", h.ID, h.Name, outcome(h.res, h.err)))
		}
	}
	if len(done) == 0 {
		return ""
	}
	s := "[gilda: " + strings.Join(done, "; ") + "; read the answers with task_collect"
	if running > 0 {
		s += fmt.Sprintf(". %s still running", plural(running, "task"))
	}
	return s + "]"
}

// next waits for a notice. ok is false once no task is running and none is waiting to be read.
func (t *Tasks) next(ctx context.Context) (notice string, ok bool, err error) {
	for {
		if n := t.Notice(); n != "" {
			return n, true, nil
		}
		if len(t.Running()) == 0 {
			return "", false, nil
		}
		select {
		case <-t.wake:
		case <-ctx.Done():
			return "", false, ctx.Err()
		}
	}
}

// Drive runs prompt, then, while the agent has background tasks, waits for each to finish and
// tells the agent, until it answers with none left. Headless runs and tasks use it, so a task
// answers only once its own tasks have.
func Drive(ctx context.Context, a *Agent, prompt string, emit func(Event)) (Result, error) {
	res, err := a.Run(ctx, prompt, emit)
	for err == nil && a.Tasks != nil {
		notice, ok, werr := a.Tasks.next(ctx)
		if werr != nil {
			return res, werr
		}
		if !ok {
			break
		}
		r, rerr := a.Run(ctx, notice, emit)
		res.Turns += r.Turns
		res.Usage.Add(r.Usage)
		res.Text, res.Stop, err = r.Text, r.Stop, rerr
	}
	return res, err
}

// sorted orders task ids by length, then text, so t10 follows t9.
func sorted(ids []string) []string {
	slices.SortFunc(ids, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b)) })
	return ids
}

// ErrBudget means the tasks spent their token budget.
var ErrBudget = errors.New("the task token budget is used up")

// budgetFloor is the least output a request is granted. Less would cut off most responses.
const budgetFloor = 1024

// budget bounds the tokens a tree's tasks use. Each request holds its input and output grant
// until it settles at what it used, so concurrent tasks cannot together overrun it.
type budget struct {
	limit int64

	mu          sync.Mutex
	spent, held int64
	changed     chan struct{} // closed and replaced at each settle
}

// reserve grants a request output tokens. It waits while other requests' holds leave too
// little, and fails once what was spent does.
func (b *budget) reserve(ctx context.Context, input, output int64) (int64, func(*llm.Usage), error) {
	for {
		b.mu.Lock()
		if b.limit-b.spent-input < budgetFloor {
			spent := b.spent
			b.mu.Unlock()
			return 0, nil, fmt.Errorf("%w: %d of %d tokens spent, and the next request needs about %d", ErrBudget, spent, b.limit, input+budgetFloor)
		}
		if room := b.limit - b.spent - b.held - input; room >= budgetFloor {
			grant := min(output, room)
			hold := input + grant
			b.held += hold
			b.mu.Unlock()
			var once sync.Once
			return grant, func(u *llm.Usage) { once.Do(func() { b.settle(hold, u) }) }, nil
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		}
	}
}

// settle replaces a hold with what the request used. One that did not complete may have used
// its whole hold, and is charged it.
func (b *budget) settle(hold int64, u *llm.Usage) {
	b.mu.Lock()
	b.held -= hold
	if u != nil {
		b.spent += u.Input + u.Output
	} else {
		b.spent += hold
	}
	ch := b.changed
	b.changed = make(chan struct{})
	b.mu.Unlock()
	close(ch)
}

func (b *budget) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.spent = 0
	b.mu.Unlock()
}
