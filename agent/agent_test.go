package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/mock"
	"github.com/shakfu/gilda/price"
	"github.com/shakfu/gilda/tool"
)

func newAgent(t *testing.T, steps ...mock.Step) (*Agent, *mock.Provider, string) {
	t.Helper()
	root := t.TempDir()
	p := mock.New(steps...)
	a := New(Config{Provider: p, Model: "m", Tools: tool.Default(tool.Env{Root: root, Jobs: &tool.Jobs{}})})
	return a, p, root
}

func call(name string, args any) mock.Call {
	data, _ := json.Marshal(args)
	return mock.Call{Name: name, Arguments: data}
}

func usage(in, out int64, cost float64) llm.Usage {
	return llm.Usage{Input: in, Output: out, Cost: llm.Float(cost)}
}

func TestRunLoopsThroughToolsToAnAnswer(t *testing.T) {
	a, p, root := newAgent(t,
		mock.Step{Calls: []mock.Call{call("write", map[string]string{"path": "x.txt", "content": "hi"})}, Usage: usage(100, 10, 0.01)},
		mock.Step{Text: "done", Usage: usage(150, 5, 0.02)},
	)
	var events []Event
	res, err := a.Run(context.Background(), "make x", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" || res.Turns != 2 {
		t.Fatalf("result %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "x.txt")); string(data) != "hi" {
		t.Fatalf("file %q", data)
	}
	if *res.Usage.Cost != 0.03 || res.Usage.Input != 250 || a.Used != 155 {
		t.Fatalf("usage %+v used %d", res.Usage, a.Used)
	}

	// user, assistant with a call, tool results, assistant answer.
	h := a.History
	if len(h) != 4 || h[0].Role != llm.User || h[2].Role != llm.Tool || h[3].Text != "done" {
		t.Fatalf("history %+v", h)
	}
	if h[2].Results[0].CallID != h[1].Calls[0].ID || h[2].Results[0].IsError {
		t.Fatalf("result does not answer the call: %+v", h[2])
	}
	// The second request carried the whole history.
	if len(p.Requests[1].Messages) != 3 {
		t.Fatalf("second request had %d messages", len(p.Requests[1].Messages))
	}

	var kinds []string
	for _, e := range events {
		switch e.(type) {
		case ToolCall:
			kinds = append(kinds, "call")
		case ToolResult:
			kinds = append(kinds, "result")
		case Response:
			kinds = append(kinds, "response")
		}
	}
	if want := []string{"response", "call", "result", "response"}; !equal(kinds, want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
}

func TestToolErrorsGoBackToTheModel(t *testing.T) {
	a, _, _ := newAgent(t,
		mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "missing"}), call("nope", nil)}},
		mock.Step{Text: "ok"},
	)
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	results := a.History[2].Results
	if len(results) != 2 || !results[0].IsError || !results[1].IsError {
		t.Fatalf("results %+v", results)
	}
	if results[1].Content != `unknown tool "nope"` {
		t.Fatalf("got %q", results[1].Content)
	}
}

// A response cut off at max_tokens may carry truncated arguments, so its calls are answered
// with an error instead of run.
func TestTruncatedCallsAreNotRun(t *testing.T) {
	a, _, root := newAgent(t,
		mock.Step{Calls: []mock.Call{call("write", map[string]string{"path": "x", "content": "partial"})}, Stop: llm.StopMaxTokens},
		mock.Step{Text: "ok"},
	)
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Fatal("a truncated call ran")
	}
	if r := a.History[2].Results[0]; !r.IsError {
		t.Fatalf("got %+v", r)
	}
}

// A refused response may carry calls; they are answered, not run, and the run fails.
func TestRefusedCallsAreNotRun(t *testing.T) {
	a, _, root := newAgent(t,
		mock.Step{Calls: []mock.Call{call("write", map[string]string{"path": "x", "content": "partial"})}, Stop: llm.StopRefusal},
	)
	if _, err := a.Run(context.Background(), "go", nil); err == nil {
		t.Fatal("a refusal was taken as success")
	}
	if _, err := os.Stat(filepath.Join(root, "x")); err == nil {
		t.Fatal("a refused call ran")
	}
	if r := a.History[2].Results[0]; !r.IsError {
		t.Fatalf("got %+v", r)
	}
}

func TestAFailedFirstRequestTakesThePromptBack(t *testing.T) {
	a, _, _ := newAgent(t, mock.Step{Error: "boom"})
	if _, err := a.Run(context.Background(), "go", nil); err == nil {
		t.Fatal("no error")
	}
	if len(a.History) != 0 {
		t.Fatalf("history kept %d messages", len(a.History))
	}
}

// A cancel during tools must still answer every call, or the next request is invalid.
func TestCancelAnswersEveryCall(t *testing.T) {
	a, _, _ := newAgent(t, mock.Step{Calls: []mock.Call{
		call("bash", map[string]string{"command": "sleep 5"}),
		call("read", map[string]string{"path": "x"}),
	}})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := a.Run(ctx, "go", func(e Event) {
		if _, ok := e.(ToolCall); ok {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	last := a.History[len(a.History)-1]
	if last.Role != llm.Tool || len(last.Results) != 2 {
		t.Fatalf("last message %+v", last)
	}
	for _, r := range last.Results {
		if r.Content != Cancelled || !r.IsError {
			t.Fatalf("result %+v", r)
		}
	}
}

func TestMaxTurnsStopsTheLoop(t *testing.T) {
	step := mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "."})}}
	a, _, _ := newAgent(t, step, step, step)
	a.MaxTurns = 2
	res, err := a.Run(context.Background(), "go", nil)
	if err == nil || res.Turns != 2 {
		t.Fatalf("turns %d err %v", res.Turns, err)
	}
}

func TestAFullContextIsRefusedBeforeSending(t *testing.T) {
	a, p, _ := newAgent(t, mock.Step{Text: "a", Usage: usage(96, 0, 0)}, mock.Step{Text: "b"})
	a.Context = 100
	if _, err := a.Run(context.Background(), "one", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "two", nil); !errors.Is(err, ErrContextFull) {
		t.Fatalf("err %v", err)
	}
	if len(p.Requests) != 1 || len(a.History) != 2 {
		t.Fatalf("requests %d history %d", len(p.Requests), len(a.History))
	}
	a.Reset()
	if _, err := a.Run(context.Background(), "three", nil); err != nil {
		t.Fatalf("after reset: %v", err)
	}
}

func TestCostIsEstimatedWhenTheProviderReportsNone(t *testing.T) {
	a, _, _ := newAgent(t, mock.Step{Text: "a", Usage: llm.Usage{Input: 1000, Output: 100, CacheRead: 400}})
	a.Prices = &price.Catalog{Models: map[string]price.Entry{
		"m": {Rates: &price.Rates{Prompt: 1e-6, Completion: 1e-5, CacheRead: 1e-7}},
	}}
	// The mock provider's name is "mock", which the catalog does not mirror.
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if a.Usage.Cost != nil {
		t.Fatal("estimated a cost for a provider the catalog does not list")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Anthropic rejects an empty assistant message on replay, so an empty reply must not enter the
// history as one.
func TestAnEmptyReplyIsRecordedWithText(t *testing.T) {
	a, _, _ := newAgent(t, mock.Step{}, mock.Step{Text: "next"})
	if _, err := a.Run(context.Background(), "one", nil); err != nil {
		t.Fatal(err)
	}
	if m := a.History[1]; m.Text == "" || m.Native != nil {
		t.Fatalf("empty reply recorded as %+v", m)
	}
}

// The turns a prompt already completed stay when the window fills mid-prompt: their tools have
// changed files the model must remember.
func TestAFullContextMidPromptKeepsCompletedTurns(t *testing.T) {
	a, _, _ := newAgent(t, mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "."})}, Usage: usage(96, 0, 0)})
	a.Context = 100
	if _, err := a.Run(context.Background(), "go", nil); !errors.Is(err, ErrContextFull) {
		t.Fatalf("err %v", err)
	}
	if len(a.History) != 3 || a.History[2].Role != llm.Tool {
		t.Fatalf("history %+v", a.History)
	}
	if !ContextFull(err2(llm.ErrContext)) {
		t.Fatal("a provider's context error is not recognised")
	}
}

func err2(e error) error { return errors.Join(errors.New("400"), e) }

func TestApproveGatesEachCall(t *testing.T) {
	a, _, root := newAgent(t,
		mock.Step{Calls: []mock.Call{
			call("write", map[string]string{"path": "yes.txt", "content": "1"}),
			call("write", map[string]string{"path": "no.txt", "content": "2"}),
			call("bash", map[string]string{"command": "echo hi"}),
		}},
		mock.Step{Text: "ok"},
	)
	var asked []string
	a.Approve = func(_ context.Context, _ tool.Tool, c llm.ToolCall, label string) (bool, error) {
		asked = append(asked, label)
		switch label {
		case "write no.txt":
			return false, nil
		case "$ echo hi":
			return false, errors.New("dialog closed")
		}
		return true, nil
	}
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 3 {
		t.Fatalf("asked %v", asked)
	}
	if _, err := os.Stat(filepath.Join(root, "yes.txt")); err != nil {
		t.Fatal("an approved call did not run")
	}
	if _, err := os.Stat(filepath.Join(root, "no.txt")); err == nil {
		t.Fatal("a declined call ran")
	}
	r := a.History[2].Results
	if r[0].IsError || r[1].Content != Declined || !r[1].IsError || !strings.Contains(r[2].Content, "dialog closed") {
		t.Fatalf("results %+v", r)
	}
}

func TestRecordsAreJSONReady(t *testing.T) {
	cases := []struct {
		ev   Event
		want string
	}{
		{Text{"hi"}, `{"text":"hi","type":"text"}`},
		{Retry{Attempt: 2, Reason: "529 status code 529"}, `{"attempt":2,"reason":"529 status code 529","type":"retry"}`},
		{Elided{Results: 3, Bytes: 9000}, `{"bytes":9000,"results":3,"type":"elided"}`},
		{ToolCall{Call: llm.ToolCall{ID: "1", Name: "read", Arguments: `{"path":"a"}`}, Label: "read a"},
			`{"arguments":{"path":"a"},"id":"1","label":"read a","name":"read","type":"tool_call"}`},
		{ToolCall{Call: llm.ToolCall{ID: "2", Name: "read", Arguments: `{"pa`}},
			`{"arguments":"{\"pa","id":"2","label":"","name":"read","type":"tool_call"}`},
		{ToolResult{Call: llm.ToolCall{ID: "1", Name: "read"}, Label: "read a", Err: errors.New("no such file")},
			`{"error":"no such file","id":"1","label":"read a","name":"read","ok":false,"output":"","summary":"","type":"tool_result"}`},
	}
	for _, c := range cases {
		data, err := json.Marshal(Record(c.ev))
		if err != nil || string(data) != c.want {
			t.Errorf("got  %s\nwant %s (%v)", data, c.want, err)
		}
	}
}

// A stream cut off mid-response is sent again, up to maxCut times in a row, without leaving
// the cut response in the history.
func TestACutStreamIsSentAgain(t *testing.T) {
	a, p, _ := newAgent(t, mock.Step{Text: "part", Incomplete: true}, mock.Step{Text: "whole"})
	var retries []Retry
	res, err := a.Run(context.Background(), "go", func(e Event) {
		if r, ok := e.(Retry); ok {
			retries = append(retries, r)
		}
	})
	if err != nil || res.Text != "whole" || res.Turns != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(retries) != 1 || retries[0].Attempt != 1 || len(a.History) != 2 || len(p.Requests) != 2 {
		t.Fatalf("retries %v history %d requests %d", retries, len(a.History), len(p.Requests))
	}
	cut := mock.Step{Incomplete: true}
	a, _, _ = newAgent(t, cut, cut, cut, mock.Step{Text: "never"})
	if _, err := a.Run(context.Background(), "go", nil); !errors.Is(err, llm.ErrIncomplete) {
		t.Fatalf("after %d resends: %v", maxCut, err)
	}
}

// Each resend sends the whole request again, so StreamRetries can turn them off.
func TestStreamRetriesCanBeTurnedOff(t *testing.T) {
	if got := New(Config{StreamRetries: -1}).StreamRetries; got != 0 {
		t.Fatalf("negative became %d", got)
	}
	a, p, _ := newAgent(t, mock.Step{Text: "part", Incomplete: true}, mock.Step{Text: "whole"})
	a.StreamRetries = 0
	if _, err := a.Run(context.Background(), "go", nil); !errors.Is(err, llm.ErrIncomplete) || len(p.Requests) != 1 {
		t.Fatalf("err %v after %d requests", err, len(p.Requests))
	}
}

// A symlink swapped while the user decides cannot redirect an approved write: it goes to the
// file approval saw. A declined
// call's bind error, which can reveal an unapproved file's content, does not reach the model.
func TestApprovalBindsTheTarget(t *testing.T) {
	a, _, root := newAgent(t,
		mock.Step{Calls: []mock.Call{
			call("write", map[string]string{"path": "link", "content": "new"}),
			call("edit", map[string]string{"path": "secret", "old_string": "guess", "new_string": "x"}),
		}},
		mock.Step{Text: "ok"},
	)
	outside := filepath.Join(t.TempDir(), "outside")
	for path, text := range map[string]string{filepath.Join(root, "f"): "f", outside: "outside", filepath.Join(root, "secret"): "s"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink("f", link); err != nil {
		t.Fatal(err)
	}
	var paths []string
	a.Approve = func(_ context.Context, tl tool.Tool, c llm.ToolCall, _ string) (bool, error) {
		if c.Name == "edit" {
			return false, nil
		}
		paths, _ = tl.(tool.Paths).Paths(json.RawMessage(c.Arguments))
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		return true, nil
	}
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(root, "f"))
	if !slices.Contains(paths, real) {
		t.Errorf("approval saw %v, not the resolved %s", paths, real)
	}
	r := a.History[2].Results
	if r[0].IsError {
		t.Errorf("write through a swapped link: %+v", r[0])
	}
	if r[1].Content != Declined {
		t.Errorf("declined edit: %+v", r[1])
	}
	for path, want := range map[string]string{filepath.Join(root, "f"): "new", outside: "outside"} {
		if data, _ := os.ReadFile(path); string(data) != want {
			t.Errorf("%s is %q", path, data)
		}
	}
}

// readStep is a mock step that reads path and reports in input tokens.
func readStep(path string, in int64) mock.Step {
	return mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": path})}, Usage: usage(in, 0, 0)}
}

func TestOldToolResultsAreElidedPastSeventyPercent(t *testing.T) {
	steps := []mock.Step{}
	for i := 0; i < 6; i++ {
		steps = append(steps, readStep("big.txt", 100))
	}
	// The window is 10k tokens; usage passes 70% on the last round-trip before the answer.
	steps[5].Usage = usage(7500, 0, 0)
	steps = append(steps, mock.Step{Text: "done", Usage: usage(3000, 0, 0)})
	a, p, root := newAgent(t, steps...)
	a.Context = 10_000
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("x\n", 3000)), 0o644); err != nil {
		t.Fatal(err)
	}
	var elided []Elided
	if _, err := a.Run(context.Background(), "go", func(e Event) {
		if e, ok := e.(Elided); ok {
			elided = append(elided, e)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(elided) != 1 || elided[0].Results != 2 {
		t.Fatalf("elided %+v", elided)
	}
	// The last request saw the two oldest results as stubs and the newest four whole.
	last := p.Requests[len(p.Requests)-1].Messages
	var stubs, whole int
	for _, m := range last {
		for _, r := range m.Results {
			switch {
			case strings.HasPrefix(r.Content, "[gilda: elided ") && strings.Contains(r.Content, "of read output"):
				stubs++
			case len(r.Content) > elideMin:
				whole++
			}
		}
	}
	if stubs != 2 || whole != 4 {
		t.Fatalf("stubs %d whole %d", stubs, whole)
	}
	if a.Used != 3000 {
		t.Fatalf("used %d: the next response's usage replaces the estimate", a.Used)
	}
}

// Eliding a little at a time would break the prompt cache on every turn, so an elision that
// frees under a tenth of the window is not done.
func TestASmallElisionIsSkipped(t *testing.T) {
	steps := []mock.Step{}
	for i := 0; i < 5; i++ {
		steps = append(steps, readStep("small.txt", 8000))
	}
	steps = append(steps, mock.Step{Text: "done"})
	a, p, root := newAgent(t, steps...)
	a.Context = 10_000
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte(strings.Repeat("y\n", 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Requests[len(p.Requests)-1].Messages {
		for _, r := range m.Results {
			if isStub(r.Content) {
				t.Fatal("a result was elided for a small gain")
			}
		}
	}
}

func TestElisionCanBeTurnedOff(t *testing.T) {
	a, _, _ := newAgent(t)
	a.KeepResults = -1
	a.Context = 100
	a.History = []llm.Message{{Role: llm.Tool, Results: []llm.ToolResult{{Content: strings.Repeat("z", 5000)}}}}
	if n, _, _ := a.elide(); n != 0 {
		t.Fatalf("elided %d", n)
	}
}

// Past 85% the model is told once, in the message about to be sent, so the cached prefix holds.
func TestTheModelIsWarnedOnceNearAFullWindow(t *testing.T) {
	a, p, _ := newAgent(t,
		mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "."})}, Usage: usage(90, 0, 0)},
		mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "."})}, Usage: usage(91, 0, 0)},
		mock.Step{Text: "done", Usage: usage(92, 0, 0)},
	)
	a.Context = 100
	if _, err := a.Run(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, m := range p.Requests[2].Messages {
		for _, r := range m.Results {
			notes += strings.Count(r.Content, "of the context window")
		}
	}
	if notes != 1 || !strings.Contains(p.Requests[1].Messages[2].Results[0].Content, "90% of the context window") {
		t.Fatalf("notes %d, history %+v", notes, p.Requests[1].Messages)
	}
}

// Images stay in the history but reach only a model that accepts them.
func TestImagesReachOnlyAModelThatAcceptsThem(t *testing.T) {
	img := llm.Image{MediaType: "image/png", Data: []byte("PNG")}
	pic, err := tool.New(tool.Def{Name: "pic", ReadOnly: true, Run: func(context.Context, json.RawMessage) (tool.Result, error) {
		return tool.Result{Output: "a picture", Images: []llm.Image{img}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, accepts := range []bool{false, true} {
		p := mock.New(mock.Step{Calls: []mock.Call{{Name: "pic"}}}, mock.Step{Text: "seen"})
		a := New(Config{Provider: p, Model: "m", Tools: []tool.Tool{pic}, Images: accepts})
		if _, err := a.Run(context.Background(), "look", nil); err != nil {
			t.Fatal(err)
		}
		sent := p.Requests[1].Messages[2].Results[0]
		kept := a.History[2].Results[0]
		if len(kept.Images) != 1 || kept.Content != "a picture" {
			t.Fatalf("accepts %v: history %+v", accepts, kept)
		}
		if accepts != (len(sent.Images) == 1) || accepts == strings.Contains(sent.Content, "not sent") {
			t.Fatalf("accepts %v: sent %+v", accepts, sent)
		}
	}
}

func TestOldImagesAreElided(t *testing.T) {
	a, _, _ := newAgent(t)
	a.Context = 10_000
	img := llm.ToolResult{CallID: "c", Content: "a picture", Images: []llm.Image{{MediaType: "image/png", Data: []byte("PNG")}}}
	for range keepResults + 1 {
		a.History = append(a.History, llm.Message{Role: llm.Tool, Results: []llm.ToolResult{img}})
	}
	n, _, tokens := a.elide()
	if n != 1 || tokens != imageTokens+int64(len("a picture")/bytesPerToken) || a.History[0].Results[0].Images != nil || !isStub(a.History[0].Results[0].Content) {
		t.Fatalf("elided %d (%d tokens): %+v", n, tokens, a.History[0])
	}
	if len(a.History[keepResults].Results[0].Images) != 1 {
		t.Fatal("a recent image was elided")
	}
}

// A subagent's work stays out of the caller's history; its answer, its calls and its usage
// reach the caller.
func TestTaskRunsASubagent(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte(strings.Repeat("filler\n", 50)+"func Needle() {}\n"), 0o644)
	child := mock.New(
		mock.Step{Calls: []mock.Call{call("read", map[string]string{"path": "a.go"})}, Usage: usage(50, 5, 0.01)},
		mock.Step{Text: "Needle is defined at a.go:51.", Usage: usage(80, 10, 0.02)},
	)
	task := Task{New: func() *Agent {
		return New(Config{Provider: child, Model: "m", Tools: []tool.Tool{tool.Read{Env: tool.Env{Root: root}}}})
	}}
	parent := mock.New(
		mock.Step{Calls: []mock.Call{call("task", map[string]string{"prompt": "find Needle"})}, Usage: usage(100, 5, 0.1)},
		mock.Step{Text: "It is in a.go.", Usage: usage(120, 5, 0.1)},
	)
	a := New(Config{Provider: parent, Model: "m", Tools: []tool.Tool{task}})
	var labels []string
	var done []TaskDone
	res, err := a.Run(context.Background(), "where is Needle?", func(e Event) {
		switch e := e.(type) {
		case ToolResult:
			labels = append(labels, e.Label)
		case TaskDone:
			done = append(done, e)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.History[2].Results[0].Content; got != "Needle is defined at a.go:51." {
		t.Fatalf("task result %q", got)
	}
	for _, m := range parent.Requests[1].Messages {
		for _, r := range m.Results {
			if strings.Contains(r.Content, "filler") {
				t.Fatal("the subagent's read reached the caller")
			}
		}
	}
	if strings.Join(labels, "|") != "[task] read a.go|task find Needle" {
		t.Fatalf("labels %v", labels)
	}
	if len(done) != 1 || done[0].Turns != 2 || done[0].Usage.Input != 130 {
		t.Fatalf("done %+v", done)
	}
	if res.Usage.Input != 350 || a.Usage.Input != 350 || *res.Usage.Cost < 0.229 {
		t.Fatalf("run usage %+v session %+v", res.Usage, a.Usage)
	}
}

func TestATaskThatStopsEarlyReportsWhatItHas(t *testing.T) {
	child := mock.New(mock.Step{Text: "partial", Calls: []mock.Call{call("read", map[string]string{"path": "x"})}})
	task := Task{New: func() *Agent {
		return New(Config{Provider: child, Model: "m", MaxTurns: 1, Tools: tool.Default(tool.Env{Root: t.TempDir()})})
	}}
	out, err := task.Run(context.Background(), json.RawMessage(`{"prompt":"go"}`))
	if err != nil || !out.Failed || !strings.Contains(out.Output, "partial") || !strings.Contains(out.Output, "stopped early") {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := task.Run(context.Background(), json.RawMessage(`{"prompt":" "}`)); err == nil {
		t.Fatal("an empty prompt was accepted")
	}
}
