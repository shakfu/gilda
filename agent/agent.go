// Package agent runs the tool loop: send the history, stream the reply, run the tools it asks
// for, send the results, until the model answers without a tool call.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/price"
	"github.com/shakfu/gilda/tool"
)

// maxCut is the default bound on resends of one round-trip whose stream ended early.
const maxCut = 2

// Declined is the result recorded for a call Approve refused.
const Declined = "the user declined this call"

// Cancelled is the result recorded for a call that a cancel stopped or skipped, so the history
// stays valid for the next prompt.
const Cancelled = "cancelled by the user"

// ErrContextFull means the last request came within 5% of the model's window, after old tool
// results were elided.
var ErrContextFull = errors.New("context window is full; start a new conversation")

var errRefused = errors.New("the model refused the request")

// ContextFull reports whether err means the conversation no longer fits, whether gilda refused
// the request or the provider did.
func ContextFull(err error) bool {
	return errors.Is(err, ErrContextFull) || errors.Is(err, llm.ErrContext)
}

type Config struct {
	Provider llm.Provider
	Model    string
	System   string
	Tools    []tool.Tool
	// MaxTokens caps one response. Default 32000.
	MaxTokens int64
	Effort    string
	// MaxTurns caps provider round-trips per prompt. Default 64.
	MaxTurns int
	// StreamRetries bounds the resends of a round-trip whose stream ended early. Each resend
	// sends the whole request again. Default 2; negative means none.
	StreamRetries int
	// OutputCap bounds one tool result in bytes. Default tool.OutputCap.
	OutputCap int
	// Context is the model's window in tokens; 0 when unknown, which turns off elision and the
	// full-window check.
	Context int64
	// Images is set when the model accepts images. Otherwise images that tools returned stay in
	// the history, for a later model that does, but each request carries a note in their place.
	Images bool
	// KeepResults is how many recent tool messages keep their results whole when the window
	// passes 70% and older results are elided. Default 4; negative turns elision off.
	KeepResults int
	// Prices estimates cost for providers that report none. May be nil.
	Prices *price.Catalog
	// SessionID keys prompt caching.
	SessionID string
	// Approve, when set, is asked before each call runs; label is what Label reports, such as
	// "$ go test". A refusal goes back to the model as Declined, an error as its text. Nil runs
	// every call; permission.Approver builds one from a mode.
	Approve func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string) (bool, error)
}

type Agent struct {
	Config
	History []llm.Message
	// Usage totals the session, across /clear.
	Usage llm.Usage
	// Used is the context the last request filled: its prompt plus its output. After an
	// elision it is an estimate until the next response.
	Used int64
	// warned is set once the model has been told the window is nearly full.
	warned bool
}

func New(cfg Config) *Agent {
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 32000
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 64
	}
	switch {
	case cfg.StreamRetries == 0:
		cfg.StreamRetries = maxCut
	case cfg.StreamRetries < 0:
		cfg.StreamRetries = 0
	}
	if cfg.OutputCap == 0 {
		cfg.OutputCap = tool.OutputCap
	}
	return &Agent{Config: cfg}
}

// Event is one of the types below, reported as a run progresses.
type Event interface{ event() }

type (
	Text      struct{ Text string }
	Reasoning struct{ Text string }
	// ToolStart fires when the model opens a call, before its arguments arrive.
	ToolStart struct{ Name string }
	ToolCall  struct {
		Call  llm.ToolCall
		Label string
	}
	ToolResult struct {
		Call   llm.ToolCall
		Label  string
		Result tool.Result
		// Err is set when the tool failed; its text is what the model received.
		Err error
	}
	// Retry reports that the provider's SDK is sending a request again: Attempt counts from 1,
	// and Reason is why the previous attempt failed, such as "429 Too Many Requests".
	Retry struct {
		Attempt int
		Reason  string
	}
	// Elided reports that Results old tool results, Bytes in all, were replaced by stubs to
	// free context.
	Elided struct {
		Results, Bytes int
	}
	// TaskDone reports a subagent's usage, which the run and the session include.
	TaskDone struct {
		Usage llm.Usage
		Turns int
	}
	// Response closes one provider round-trip.
	Response struct {
		Text  string
		Usage llm.Usage
		Stop  llm.StopReason
	}
)

func (Text) event()       {}
func (Reasoning) event()  {}
func (ToolStart) event()  {}
func (ToolCall) event()   {}
func (ToolResult) event() {}
func (Response) event()   {}
func (Retry) event()      {}
func (Elided) event()     {}
func (TaskDone) event()   {}

// Result summarises one prompt.
type Result struct {
	Text  string
	Turns int
	Usage llm.Usage
	// Stop is the last response's stop reason. StopMaxTokens with a nil error means Text was
	// cut off at the output limit.
	Stop llm.StopReason
}

// Reset starts a new conversation. Session usage is kept.
func (a *Agent) Reset() {
	a.History = nil
	a.Used = 0
	a.warned = false
}

// Run sends prompt and loops until the model stops calling tools. On a failure before the model
// replied, the prompt is taken back out of the history.
func (a *Agent) Run(ctx context.Context, prompt string, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	start := len(a.History)
	a.History = append(a.History, llm.Message{Role: llm.User, Text: prompt})
	var res Result
	cut := 0 // consecutive round-trips whose stream ended early
	for res.Turns < a.MaxTurns {
		if a.manageContext(emit) {
			// Completed turns stay: their tools already changed files the model must remember.
			if len(a.History) == start+1 {
				a.History = a.History[:start]
			}
			return res, ErrContextFull
		}
		rctx := llm.WithRetries(ctx, func(attempt int, reason string) {
			emit(Retry{Attempt: attempt, Reason: reason})
		})
		resp, err := a.Provider.Stream(rctx, a.request(), func(e llm.Event) {
			switch e.Kind {
			case llm.TextDelta:
				emit(Text{e.Text})
			case llm.ReasoningDelta:
				emit(Reasoning{e.Text})
			case llm.ToolStart:
				emit(ToolStart{e.Text})
			}
		})
		// A stream that ended early left no trace in the history, so the same request can go
		// again. The SDKs retry failed requests but not a response cut off mid-stream.
		if errors.Is(err, llm.ErrIncomplete) && ctx.Err() == nil && cut < a.StreamRetries {
			cut++
			emit(Retry{Attempt: cut, Reason: err.Error()})
			continue
		}
		if err != nil {
			if len(a.History) == start+1 {
				a.History = a.History[:start]
			}
			return res, err
		}
		cut = 0
		res.Turns++
		a.price(&resp.Usage)
		a.Usage.Add(resp.Usage)
		res.Usage.Add(resp.Usage)
		a.Used = resp.Usage.Input + resp.Usage.Output

		msg := resp.Message
		if msg.Text == "" && len(msg.Calls) == 0 {
			// Anthropic rejects an assistant message with no content, and a reasoning-only
			// Responses item needs a following item, so an empty reply would break every later
			// request.
			msg.Text, msg.Native = "(no response)", nil
		}
		for i := range msg.Calls {
			// Some local servers send calls without ids; results must still address them.
			if msg.Calls[i].ID == "" {
				msg.Calls[i].ID = fmt.Sprintf("call_%d_%d", len(a.History), i)
			}
		}
		a.History = append(a.History, msg)
		res.Text, res.Stop = resp.Message.Text, resp.Stop
		emit(Response{Text: resp.Message.Text, Usage: resp.Usage, Stop: resp.Stop})

		// Calls in a cut-off or refused response are answered with this error instead of run.
		var skip error
		switch resp.Stop {
		case llm.StopMaxTokens:
			skip = errors.New("response was cut off at the output limit; retry with a smaller call")
		case llm.StopRefusal:
			skip = errRefused
		}
		if len(msg.Calls) > 0 {
			tctx := context.WithValue(ctx, runKey{}, &runState{agent: a, res: &res, emit: emit})
			results, err := a.runTools(tctx, msg.Calls, skip, emit)
			a.History = append(a.History, llm.Message{Role: llm.Tool, Results: results})
			if err != nil {
				return res, err
			}
		}
		switch {
		case resp.Stop == llm.StopRefusal:
			return res, errRefused
		case len(msg.Calls) == 0:
			return res, nil
		}
	}
	return res, fmt.Errorf("stopped after %d round-trips; raise --max-turns or continue with a new prompt", a.MaxTurns)
}

func (a *Agent) request() llm.Request {
	return llm.Request{
		Model:     a.Model,
		System:    a.System,
		Messages:  a.messages(),
		Tools:     tool.Specs(a.Tools),
		MaxTokens: a.MaxTokens,
		Effort:    a.Effort,
		SessionID: a.SessionID,
	}
}

// imageNote replaces images for a model that does not accept them.
const imageNote = "\n[gilda: the image was not sent, since this model does not accept images]"

// messages is the history as the model receives it: without images unless it accepts them.
func (a *Agent) messages() []llm.Message {
	if a.Images {
		return a.History
	}
	var out []llm.Message
	for i, m := range a.History {
		if !hasImages(m) {
			continue
		}
		if out == nil {
			out = slices.Clone(a.History)
		}
		rs := slices.Clone(m.Results)
		for j := range rs {
			if len(rs[j].Images) > 0 {
				rs[j].Images, rs[j].Content = nil, rs[j].Content+imageNote
			}
		}
		out[i].Results = rs
	}
	if out == nil {
		return a.History
	}
	return out
}

func hasImages(m llm.Message) bool {
	for _, r := range m.Results {
		if len(r.Images) > 0 {
			return true
		}
	}
	return false
}

// runTools runs calls in order. Every call gets a result, even after a cancel, because the next
// request must answer each one.
func (a *Agent) runTools(ctx context.Context, calls []llm.ToolCall, skip error, emit func(Event)) ([]llm.ToolResult, error) {
	results := make([]llm.ToolResult, 0, len(calls))
	for _, c := range calls {
		if ctx.Err() != nil {
			results = append(results, llm.ToolResult{CallID: c.ID, Content: Cancelled, IsError: true})
			continue
		}
		t := tool.Find(a.Tools, c.Name)
		args := json.RawMessage(c.Arguments)
		var label string
		if t != nil {
			label = t.Label(args)
		} else {
			label = c.Name
		}
		emit(ToolCall{Call: c, Label: label})

		var out tool.Result
		var err error
		switch {
		case skip != nil:
			err = skip
		case t == nil:
			err = fmt.Errorf("unknown tool %q", c.Name)
		default:
			out, err = a.run(ctx, t, c, label, args)
		}
		if err != nil && ctx.Err() != nil {
			err = errors.New(Cancelled)
		}
		emit(ToolResult{Call: c, Label: label, Result: out, Err: err})
		if err != nil {
			results = append(results, llm.ToolResult{CallID: c.ID, Content: err.Error(), IsError: true})
		} else {
			results = append(results, llm.ToolResult{CallID: c.ID, Content: tool.Cap(out.Output, a.OutputCap), Images: out.Images})
		}
	}
	return results, ctx.Err()
}

// run binds the call when the tool is a tool.Binder, asks Approve, when set, then runs it.
func (a *Agent) run(ctx context.Context, t tool.Tool, c llm.ToolCall, label string, args json.RawMessage) (tool.Result, error) {
	var bindErr error
	if b, ok := t.(tool.Binder); ok {
		if bound, err := b.Bind(args); err != nil {
			bindErr = err
		} else {
			t = bound
		}
	}
	if a.Approve != nil {
		ok, err := a.Approve(ctx, t, c, label)
		switch {
		case err != nil:
			return tool.Result{}, err
		case !ok:
			return tool.Result{}, errors.New(Declined)
		}
	}
	if bindErr != nil {
		return tool.Result{}, bindErr
	}
	return t.Run(ctx, args)
}

// price fills in an estimated cost when the provider reported none.
func (a *Agent) price(u *llm.Usage) {
	if u.Cost != nil {
		return
	}
	if e, ok := a.Prices.Lookup(a.Provider.Name(), a.Model); ok {
		if c, ok := e.Cost(*u); ok {
			u.Cost, u.Estimated = &c, true
		}
	}
}
