package anthropic

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/llmtest"
)

var turnWithThinkingAndCall = llmtest.SSE(
	"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-x","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"cache_read_input_tokens":90,"cache_creation_input_tokens":5,"output_tokens":1}}}`,
	"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
	"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}`,
	"content_block_stop", `{"type":"content_block_stop","index":0}`,
	"content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
	"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}`,
	"content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}`,
	"content_block_stop", `{"type":"content_block_stop","index":1}`,
	"content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`,
	"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
	"content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a\"}"}}`,
	"content_block_stop", `{"type":"content_block_stop","index":2}`,
	"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":20}}`,
	"message_stop", `{"type":"message_stop"}`,
)

var plainAnswer = llmtest.SSE(
	"message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-x","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":1}}}`,
	"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
	"content_block_stop", `{"type":"content_block_stop","index":0}`,
	"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`,
	"message_stop", `{"type":"message_stop"}`,
)

func request(model string, history ...llm.Message) llm.Request {
	return llm.Request{
		Model:     model,
		System:    "be terse",
		Messages:  history,
		MaxTokens: 1000,
		Effort:    "high",
		Tools: []llm.ToolSpec{{Name: "read", Description: "read a file", Schema: map[string]any{
			"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"},
		}}},
	}
}

func TestStreamAssemblesTextCallsUsageAndReasoning(t *testing.T) {
	srv := llmtest.New(t, turnWithThinkingAndCall)
	p := New("anthropic", "k", srv.URL)
	var text, reasoning strings.Builder
	var started []string
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("claude-x", user), func(e llm.Event) {
		switch e.Kind {
		case llm.TextDelta:
			text.WriteString(e.Text)
		case llm.ReasoningDelta:
			reasoning.WriteString(e.Text)
		case llm.ToolStart:
			started = append(started, e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Hello" || reasoning.String() != "hmm" || len(started) != 1 || started[0] != "read" {
		t.Fatalf("events: text %q reasoning %q started %v", text.String(), reasoning.String(), started)
	}
	m := resp.Message
	if m.Text != "Hello" || len(m.Calls) != 1 || m.Calls[0].Arguments != `{"path":"a"}` || m.Calls[0].ID != "toolu_1" {
		t.Fatalf("message %+v", m)
	}
	if resp.Stop != llm.StopToolUse {
		t.Fatalf("stop %q", resp.Stop)
	}
	u := resp.Usage
	if u.Input != 105 || u.CacheRead != 90 || u.CacheWrite != 5 || u.Output != 20 {
		t.Fatalf("usage %+v", u)
	}

	body := srv.Bodies[0]
	if llmtest.Get(body, "cache_control", "type") != "ephemeral" {
		t.Error("no top-level cache_control")
	}
	if llmtest.Get(body, "system", 0, "cache_control", "type") != "ephemeral" {
		t.Error("system block is not a cache breakpoint")
	}
	if llmtest.Get(body, "tools", 0, "eager_input_streaming") != true {
		t.Error("tool input does not stream eagerly")
	}
	if llmtest.Get(body, "output_config", "effort") != "high" {
		t.Error("effort not sent")
	}
	if llmtest.Get(body, "stream") != true {
		t.Error("request is not streamed")
	}
}

// Thinking blocks and their signatures replay unchanged to the model that produced them. After
// a model switch the message is rebuilt from text and tool calls.
func TestNativeHistoryReplaysOnlyToTheSameModel(t *testing.T) {
	srv := llmtest.New(t, turnWithThinkingAndCall, plainAnswer, plainAnswer)
	p := New("anthropic", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("claude-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{user, resp.Message, {Role: llm.Tool, Results: []llm.ToolResult{{CallID: "toolu_1", Content: "file text"}}}}

	for _, model := range []string{"claude-x", "claude-y"} {
		if _, err := p.Stream(context.Background(), request(model, history...), func(llm.Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	same, other := srv.Bodies[1], srv.Bodies[2]
	if llmtest.Get(same, "messages", 1, "content", 0, "type") != "thinking" ||
		llmtest.Get(same, "messages", 1, "content", 0, "signature") != "sig123" {
		t.Fatalf("same model lost the thinking block: %v", llmtest.Get(same, "messages", 1))
	}
	if llmtest.Get(other, "messages", 1, "content", 0, "type") != "text" ||
		llmtest.Get(other, "messages", 1, "content", 1, "type") != "tool_use" {
		t.Fatalf("other model got %v", llmtest.Get(other, "messages", 1))
	}
	for _, body := range []map[string]any{same, other} {
		if llmtest.Get(body, "messages", 2, "content", 0, "type") != "tool_result" ||
			llmtest.Get(body, "messages", 2, "content", 0, "tool_use_id") != "toolu_1" {
			t.Fatalf("tool result: %v", llmtest.Get(body, "messages", 2))
		}
	}
}

func TestAStreamWithoutAStopReasonIsAnError(t *testing.T) {
	cut := llmtest.SSE(
		"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
	)
	srv := llmtest.New(t, cut)
	_, err := New("anthropic", "k", srv.URL).Stream(context.Background(), request("x", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if err == nil {
		t.Fatal("a cut stream was taken as complete")
	}
}

func TestRawArgsFallsBackToAnEmptyObject(t *testing.T) {
	for in, want := range map[string]string{`{"a":1}`: `{"a":1}`, `{"a":`: `{}`, ``: `{}`, `[1]`: `{}`} {
		if got := string(rawArgs(in)); got != want {
			t.Errorf("rawArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

// Some OpenRouter upstreams issue ids such as "functions.bash:0", which Anthropic rejects.
func TestForeignCallIDsAreMadeValidOnBothSides(t *testing.T) {
	srv := llmtest.New(t, plainAnswer)
	history := []llm.Message{
		{Role: llm.User, Text: "hi"},
		{Role: llm.Assistant, Calls: []llm.ToolCall{{ID: "functions.bash:0", Name: "bash", Arguments: `{}`}}},
		{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "functions.bash:0", Content: "ok"}}},
	}
	if _, err := New("anthropic", "k", srv.URL).Stream(context.Background(), request("claude-x", history...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	use := llmtest.Get(srv.Bodies[0], "messages", 1, "content", 0, "id")
	res := llmtest.Get(srv.Bodies[0], "messages", 2, "content", 0, "tool_use_id")
	if use != "functions_bash_0" || res != use {
		t.Fatalf("tool_use %v, tool_result %v", use, res)
	}
}

// The SDK retries an overloaded request; the retry reaches the caller through the context.
func TestRetriesAreReported(t *testing.T) {
	srv := llmtest.New(t, llmtest.Status(529, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), plainAnswer)
	var reasons []string
	ctx := llm.WithRetries(context.Background(), func(_ int, r string) { reasons = append(reasons, r) })
	resp, err := New("anthropic", "k", srv.URL).Stream(ctx, request("claude-x", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if err != nil || resp.Message.Text != "done" {
		t.Fatalf("%v %+v", err, resp)
	}
	if len(reasons) != 1 || !strings.HasPrefix(reasons[0], "529") {
		t.Fatalf("reasons %v", reasons)
	}
}

// A saved and restored payload replays the same request as the one kept in memory.
func TestNativePayloadSurvivesSaving(t *testing.T) {
	srv := llmtest.New(t, turnWithThinkingAndCall, plainAnswer, plainAnswer)
	p := New("anthropic", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("claude-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	results := llm.Message{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "toolu_1", Content: "x"}}}
	for _, m := range []llm.Message{resp.Message, llmtest.Restore(t, Codec, resp.Message)} {
		h := []llm.Message{user, m, results}
		if _, err := p.Stream(context.Background(), request("claude-x", h...), func(llm.Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(srv.Bodies[1], srv.Bodies[2]) {
		t.Fatalf("in memory:\n%v\nrestored:\n%v", srv.Bodies[1], srv.Bodies[2])
	}
	if _, err := Codec.Encode("not a payload"); err == nil {
		t.Fatal("a foreign payload was encoded")
	}
	for _, bad := range []string{`"x"`, `{}`, `{"role":"user","content":[{"type":"text","text":"a"}]}`} {
		if _, err := Codec.Decode(json.RawMessage(bad)); err == nil {
			t.Errorf("decoded %s", bad)
		}
	}
}

// An image goes inside the tool_result block, after its text.
func TestToolResultImagesAreSent(t *testing.T) {
	srv := llmtest.New(t, plainAnswer)
	if _, err := New("anthropic", "k", srv.URL).Stream(context.Background(), request("claude-x", llmtest.ImageHistory("toolu_1")...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	block := llmtest.Get(srv.Bodies[0], "messages", 2, "content", 0)
	if llmtest.Get(block, "type") != "tool_result" || llmtest.Get(block, "content", 0, "text") != "a.png is a PNG image" ||
		llmtest.Get(block, "content", 1, "type") != "image" || llmtest.Get(block, "content", 1, "source", "media_type") != "image/png" ||
		llmtest.Get(block, "content", 1, "source", "data") != "UE5HREFUQQ==" {
		t.Fatalf("block %v", block)
	}
}

// A payload this adapter did not make, under its name and model, is rebuilt from text and
// calls rather than asserted.
func TestAForeignPayloadIsRebuilt(t *testing.T) {
	srv := llmtest.New(t, plainAnswer)
	h := llmtest.ImageHistory("toolu_1")
	h[1].Text = "looking"
	h[1].Native = &llm.Native{Provider: "anthropic", Model: "claude-x", Data: "not a payload"}
	if _, err := New("anthropic", "k", srv.URL).Stream(context.Background(), request("claude-x", h...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	if body, _ := json.Marshal(srv.Bodies[0]); !strings.Contains(string(body), `"looking"`) {
		t.Fatalf("the message was not rebuilt: %s", body)
	}
}
