package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/prompt"
	"github.com/shakfu/gilda/provider"
	"github.com/shakfu/gilda/tool"
)

// Options configures one run.
type Options struct {
	Dir      string
	Provider string // registry id; empty picks the first with a key
	Model    string // empty takes the registry's default
	Mode     permission.Mode
	// Parallel bounds the part agents running at once; 0 runs every part at once.
	Parallel int
	// Depth is how deep each part agent's own background tasks nest; 0 gives it none.
	Depth    int
	Running  int   // agent.TaskOptions.Running, per part
	Budget   int64 // agent.TaskOptions.Budget, per part
	MaxTurns int
	// PriceIn and PriceOut are USD per million tokens, for providers that report no cost.
	PriceIn, PriceOut float64
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// Outcome is one agent's run: a part's, or the synthesis.
type Outcome struct {
	Title  string
	Result agent.Result
	Err    error
	// Tasks are the background tasks the agent started, after its run.
	Tasks []*agent.Handle
	// Done counts the TaskDone events for those tasks.
	Done int
	// Nested counts TaskEvents that held a TaskEvent; Labels counts task_collect results that
	// named a running task's latest call. Both must stay 0.
	Nested, Labels int
	// Start and End bound the agent's run; both are zero when it never started.
	Start, End time.Time
}

// result is what another agent reads of o: its text, or why it has none, and whether it failed.
func (o Outcome) result() (string, bool) {
	text := o.Result.Text
	if o.Err != nil && strings.TrimSpace(text) == "" {
		text = "(no result: " + o.Err.Error() + ")"
	}
	return text, o.Err != nil
}

// Report is one run. Final is nil when the synthesis did not start.
type Report struct {
	Provider, Model string
	Brief           *Brief
	Parts           []Outcome
	Final           *Outcome
	Meter           Totals
	Priced          int     // responses the Pricer priced
	Charged         float64 // what it charged
	Elapsed         time.Duration
}

// Run works on the parts of b, each once the parts it depends on have finished and the rest at
// once, then on the objective from the parts' results. The error joins the parts' and the
// synthesis' errors; the report holds whatever finished.
func Run(ctx context.Context, b *Brief, o Options) (*Report, error) {
	logw := o.Log
	if logw == nil {
		logw = io.Discard
	}
	id := o.Provider
	if id == "" {
		var err error
		if id, err = provider.Choose("", nil); err != nil {
			return nil, err
		}
	}
	p, err := provider.Open(id, "", "")
	if err != nil {
		return nil, err
	}
	model := o.Model
	if model == "" {
		e, _ := provider.Find(id)
		model = e.Model
	}
	if model == "" {
		return nil, fmt.Errorf("provider %s has no default model; pass -m", id)
	}
	var window int64
	if ms, err := p.Models(ctx); err == nil {
		for _, m := range ms {
			if m.ID == model {
				window = m.Context
			}
		}
	}

	rates := &rates{in: o.PriceIn, out: o.PriceOut}
	var pricer agent.Pricer
	if o.PriceIn > 0 || o.PriceOut > 0 {
		pricer = rates
	}
	jobs := &tool.Jobs{}
	defer jobs.Kill()
	var logMu sync.Mutex
	logf := func(format string, args ...any) {
		logMu.Lock()
		fmt.Fprintf(logw, format+"\n", args...)
		logMu.Unlock()
	}
	s := &swarm{
		cfg: agent.Config{
			Provider: &meter{Provider: p},
			Model:    model,
			Context:  window,
			MaxTurns: o.MaxTurns,
			Prices:   pricer,
			Approve:  refusals(permission.Approver(o.Mode, o.Dir, nil), logf),
		},
		o: o, jobs: jobs, logf: logf,
	}
	system := prompt.Build(o.Dir, "", prompt.Options{})

	rep := &Report{Provider: id, Model: model, Brief: b, Parts: make([]Outcome, len(b.Parts))}
	start := time.Now()
	partSystem := system + "\n" + agent.TaskPrompt + "\n"
	results, failed := make([]string, len(b.Parts)), make([]bool, len(b.Parts))
	deps := make([][]int, len(b.Parts))
	for i, part := range b.Parts {
		deps[i] = part.Depends
	}
	schedule(ctx, deps, o.Parallel, func(i int) {
		part, tag := b.Parts[i], fmt.Sprintf("p%d", i+1)
		logf("[%s] start: %s", tag, part.Title)
		out := s.run(ctx, tag, part.Title, partSystem, b.partPrompt(i, results, failed), o.Depth)
		results[i], failed[i] = out.result()
		rep.Parts[i] = out
		logf("[%s] done: %s, err %v", tag, part.Title, out.Err)
	}, func(i int, err error) {
		rep.Parts[i] = Outcome{Title: b.Parts[i].Title, Err: err}
		results[i], failed[i] = rep.Parts[i].result()
	})

	var errs []error
	usable := 0
	for _, out := range rep.Parts {
		if out.Err != nil {
			errs = append(errs, fmt.Errorf("part %q: %w", out.Title, out.Err))
		}
		if strings.TrimSpace(out.Result.Text) != "" {
			usable++
		}
	}
	switch {
	case ctx.Err() != nil:
		errs = append(errs, fmt.Errorf("synthesis not started: %w", ctx.Err()))
	case usable == 0:
		errs = append(errs, errors.New("synthesis not started: no part returned a result"))
	default:
		logf("[final] start: %s", b.Title)
		final := s.run(ctx, "final", b.Title, system, b.synthesisPrompt(results, failed), 0)
		rep.Final = &final
		if final.Err != nil {
			errs = append(errs, fmt.Errorf("synthesis: %w", final.Err))
		}
	}
	rep.Elapsed = time.Since(start)
	rep.Meter = s.cfg.Provider.(*meter).totals()
	rep.Priced, rep.Charged = rates.totals()
	return rep, errors.Join(errs...)
}

// schedule calls work(i) for each part once every part in deps[i] has finished, at most n at
// once; n <= 0 means no bound. A part takes a slot only when it is ready, so parts waiting on
// others never hold the slots those others need. A part whose wait ctx ends gets skip instead.
// deps must have no cycle.
func schedule(ctx context.Context, deps [][]int, n int, work func(int), skip func(int, error)) {
	if n <= 0 {
		n = len(deps)
	}
	slots := make(chan struct{}, n)
	done := make([]chan struct{}, len(deps))
	for i := range done {
		done[i] = make(chan struct{})
	}
	var wg sync.WaitGroup
	for i := range deps {
		wg.Go(func() {
			defer close(done[i])
			for _, d := range deps[i] {
				select {
				case <-done[d]:
				case <-ctx.Done():
					skip(i, ctx.Err())
					return
				}
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				skip(i, ctx.Err())
				return
			}
			defer func() { <-slots }()
			work(i)
		})
	}
	wg.Wait()
}

// swarm holds what every agent of a run shares.
type swarm struct {
	cfg  agent.Config
	o    Options
	jobs *tool.Jobs
	logf func(string, ...any)
}

// tools are one agent's: read, and bash unless the mode is read-only, which refuses every
// bash call. Each agent gets a read record of its own, as app.App's tasks do.
func (s *swarm) tools() []tool.Tool {
	env := tool.Env{Root: s.o.Dir, Jobs: s.jobs, Seen: &tool.Seen{}}
	if s.o.Mode == permission.ReadOnly {
		return []tool.Tool{tool.Read{Env: env}}
	}
	return []tool.Tool{tool.Read{Env: env}, tool.Bash{Env: env}}
}

// refusals logs each call approve refuses. With no ask function, permission.Approver refuses
// with an error, so the error is the refusal's reason.
func refusals(approve func(context.Context, tool.Tool, llm.ToolCall, string) (bool, error), logf func(string, ...any)) func(context.Context, tool.Tool, llm.ToolCall, string) (bool, error) {
	return func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string) (bool, error) {
		ok, err := approve(ctx, t, call, label)
		if err != nil {
			logf("  %s: %v", label, err)
		}
		return ok, err
	}
}

// run drives one agent on prompt with tools, and the task tools when depth > 0. Read-only
// mode refuses every task call, so it gets no task tools.
func (s *swarm) run(ctx context.Context, tag, title, system, text string, depth int) Outcome {
	if s.o.Mode == permission.ReadOnly {
		depth = 0
	}
	out := Outcome{Title: title, Start: time.Now()}
	cfg := s.cfg
	cfg.System = system
	cfg.SessionID = "swarm-" + tag
	cfg.Tools = s.tools()
	var tasks *agent.Tasks
	var mu sync.Mutex
	if depth > 0 {
		// Each task gets a read record of its own, as app.App's do.
		build := func(parent agent.Config, sub *agent.Tasks) *agent.Agent {
			return agent.New(agent.TaskConfig(parent, s.tools(), sub))
		}
		tasks = agent.NewTasks(agent.TaskOptions{Depth: depth, Running: s.o.Running, Budget: s.o.Budget, Build: build, Tools: s.tools})
		cfg.Tools = append(cfg.Tools, agent.Task{Tasks: tasks}, agent.TaskCollect{Tasks: tasks})
		tasks.SetSink(func(e agent.Event) {
			mu.Lock()
			defer mu.Unlock()
			switch e := e.(type) {
			case agent.TaskDone:
				out.Done++
				s.logf("[%s %s] %q done, %d turns, err %v", tag, e.ID, e.Name, e.Turns, e.Err)
			case agent.TaskEvent:
				switch in := e.Event.(type) {
				case agent.TaskEvent:
					out.Nested++
				case agent.ToolCall:
					s.logf("[%s %s] %s", tag, e.ID, in.Label)
				case agent.TaskDone:
					s.logf("[%s %s>%s] %q done", tag, e.ID, in.ID, in.Name)
				}
			}
		})
	}
	a := agent.New(cfg)
	a.Tasks = tasks
	res, err := agent.Drive(ctx, a, text, func(e agent.Event) {
		if c, ok := e.(agent.ToolCall); ok {
			s.logf("[%s] %s", tag, c.Label)
		}
	})
	if tasks != nil {
		tasks.Close()
		tasks.SetSink(nil)
		res.Usage.Add(tasks.Usage())
	}
	mu.Lock()
	defer mu.Unlock()
	out.Result, out.Err, out.Tasks = res, err, tasks.List()
	out.Labels = runningLabels(a.History)
	out.End = time.Now()
	return out
}

// runningLabels counts task_collect results whose status lines name a running task's latest
// call, which only /tasks may show.
func runningLabels(h []llm.Message) int {
	names := map[string]string{}
	n := 0
	for _, m := range h {
		for _, c := range m.Calls {
			names[c.ID] = c.Name
		}
		for _, r := range m.Results {
			if names[r.CallID] == "task_collect" && strings.Contains(r.Content, " running, ") && strings.Contains(r.Content, ", last: ") {
				n++
			}
		}
	}
	return n
}

// Check lists the invariants the run broke. They hold whatever the model answers.
func (r *Report) Check() []string {
	var bad []string
	var tokens int64
	var cost float64
	outs := r.Parts
	if r.Final != nil {
		outs = append(outs[:len(outs):len(outs)], *r.Final)
	}
	for _, out := range outs {
		u := out.Result.Usage
		tokens += u.Input + u.Output
		if u.Cost != nil {
			cost += *u.Cost
		}
		q := out.Title
		for _, h := range out.Tasks {
			if strings.Contains(h.Status(), `" running, `) {
				bad = append(bad, fmt.Sprintf("%q: task %s still running after its agent returned", q, h.ID))
			}
		}
		if out.Done != len(out.Tasks) {
			bad = append(bad, fmt.Sprintf("%q: %d TaskDone events for %d background tasks", q, out.Done, len(out.Tasks)))
		}
		if out.Nested > 0 {
			bad = append(bad, fmt.Sprintf("%q: %d nested TaskEvents", q, out.Nested))
		}
		if out.Labels > 0 {
			bad = append(bad, fmt.Sprintf("%q: %d task_collect results named a running task's call", q, out.Labels))
		}
	}
	if r.Brief != nil {
		for i, part := range r.Brief.Parts {
			for _, d := range part.Depends {
				if p, dep := r.Parts[i], r.Parts[d]; !p.Start.IsZero() && (dep.End.IsZero() || p.Start.Before(dep.End)) {
					bad = append(bad, fmt.Sprintf("order: %q started before %q, which it depends on, finished", p.Title, dep.Title))
				}
			}
		}
	}
	mt := r.Meter
	if want := mt.Usage.Input + mt.Usage.Output; tokens != want {
		bad = append(bad, fmt.Sprintf("tokens: agents counted %d, the provider returned %d", tokens, want))
	}
	if r.Priced != mt.Unpriced && r.Priced != 0 {
		bad = append(bad, fmt.Sprintf("priced: the Pricer priced %d of %d responses without a cost", r.Priced, mt.Unpriced))
	}
	reported := 0.0
	if mt.Usage.Cost != nil {
		reported = *mt.Usage.Cost
	}
	if want := reported + r.Charged; math.Abs(cost-want) > 1e-9*max(1, want) {
		bad = append(bad, fmt.Sprintf("cost: agents counted $%.6f, the provider reported $%.6f and the Pricer charged $%.6f", cost, reported, r.Charged))
	}
	if len(r.Parts) > 1 && mt.Peak < 2 {
		bad = append(bad, fmt.Sprintf("parallel: at most %d request in flight", mt.Peak))
	}
	return bad
}

// Totals is what the provider returned, across every agent.
type Totals struct {
	Responses, Failed int
	// Unpriced counts responses that reported no cost.
	Unpriced int
	Usage    llm.Usage
	// Peak is the most requests in flight at once.
	Peak int
}

// meter counts what its provider returns, for every agent that shares it.
type meter struct {
	llm.Provider

	mu       sync.Mutex
	t        Totals
	inflight int
}

func (m *meter) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	m.mu.Lock()
	m.inflight++
	m.t.Peak = max(m.t.Peak, m.inflight)
	m.mu.Unlock()
	resp, err := m.Provider.Stream(ctx, req, emit)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inflight--
	if err != nil {
		m.t.Failed++
		return resp, err
	}
	m.t.Responses++
	if resp.Usage.Cost == nil {
		m.t.Unpriced++
	}
	m.t.Usage.Add(resp.Usage)
	return resp, nil
}

func (m *meter) totals() Totals {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

// rates is the app's own agent.Pricer: one rate per direction for every model.
type rates struct {
	in, out float64 // USD per million tokens

	mu      sync.Mutex
	n       int
	charged float64
}

func (r *rates) Cost(_, _ string, u llm.Usage) (float64, bool) {
	c := (float64(u.Input)*r.in + float64(u.Output)*r.out) / 1e6
	r.mu.Lock()
	r.n++
	r.charged += c
	r.mu.Unlock()
	return c, true
}

func (r *rates) totals() (int, float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n, r.charged
}
