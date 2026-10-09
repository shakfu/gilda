package compat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/llmtest"
)

var turn = llmtest.SSE(
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think"}}]}`,
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[{"index":0,"delta":{"content":"Hi"}}]}`,
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a\"}"}}]}}]}`,
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	"", `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"local","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":7,"total_tokens":57}}`,
	"", `[DONE]`,
)

func TestStreamAssemblesChunksAndReasoning(t *testing.T) {
	srv := llmtest.New(t, turn)
	p := New("llamacpp", "", srv.URL)
	var reasoning strings.Builder
	req := llm.Request{Model: "local", System: "sys", MaxTokens: 100, Messages: []llm.Message{
		{Role: llm.User, Text: "hi"},
		{Role: llm.Assistant, Text: "earlier", Calls: []llm.ToolCall{{ID: "c0", Name: "bash", Arguments: `{"command":"ls"}`}}},
		{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "c0", Content: "a.go"}}},
	}}
	resp, err := p.Stream(context.Background(), req, func(e llm.Event) {
		if e.Kind == llm.ReasoningDelta {
			reasoning.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.String() != "think" {
		t.Fatalf("reasoning %q", reasoning.String())
	}
	m := resp.Message
	if m.Text != "Hi" || len(m.Calls) != 1 || m.Calls[0].Arguments != `{"path":"a"}` || resp.Stop != llm.StopToolUse {
		t.Fatalf("message %+v stop %q", m, resp.Stop)
	}
	if resp.Usage.Input != 50 || resp.Usage.Output != 7 {
		t.Fatalf("usage %+v", resp.Usage)
	}
	body := srv.Bodies[0]
	if llmtest.Get(body, "messages", 0, "role") != "system" ||
		llmtest.Get(body, "messages", 2, "tool_calls", 0, "function", "name") != "bash" ||
		llmtest.Get(body, "messages", 3, "role") != "tool" ||
		llmtest.Get(body, "stream_options", "include_usage") != true {
		t.Fatalf("body %v", body)
	}
}

func TestAStreamWithoutAFinishReasonIsAnError(t *testing.T) {
	srv := llmtest.New(t, llmtest.SSE("", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"}}]}`, "", "[DONE]"))
	_, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Text: "hi"}}}, func(llm.Event) {})
	if err == nil {
		t.Fatal("a cut stream was taken as complete")
	}
}

// A response cut off at the length limit may carry truncated arguments; the stop reason must
// say so, or the agent runs the call.
func TestLengthWinsOverToolCalls(t *testing.T) {
	srv := llmtest.New(t, llmtest.SSE(
		"", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"write","arguments":"{\"pa"}}]}}]}`,
		"", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
		"", "[DONE]"))
	resp, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Text: "hi"}}}, func(llm.Event) {})
	if err != nil || resp.Stop != llm.StopMaxTokens {
		t.Fatalf("stop %q err %v", resp.Stop, err)
	}
}

// A refusal or a filtered response must reach the agent as a refusal, not as a finished answer.
func TestRefusalsAndFilteredResponsesStopAsRefusals(t *testing.T) {
	const chunk = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`
	cases := map[string]struct{ delta, finish, text string }{
		"refusal":        {`{"refusal":"No."}`, `"stop"`, "No."},
		"content filter": {`{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"write","arguments":"{\"pa"}}]}`, `"content_filter"`, ""},
	}
	for name, c := range cases {
		srv := llmtest.New(t, llmtest.SSE("", fmt.Sprintf(chunk, c.delta, c.finish), "", "[DONE]"))
		var streamed strings.Builder
		resp, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Text: "hi"}}}, func(e llm.Event) {
			if e.Kind == llm.TextDelta {
				streamed.WriteString(e.Text)
			}
		})
		if err != nil || resp.Stop != llm.StopRefusal || resp.Message.Text != c.text || streamed.String() != c.text {
			t.Errorf("%s: stop %q text %q streamed %q err %v", name, resp.Stop, resp.Message.Text, streamed.String(), err)
		}
	}
}

// Chat Completions takes images only from the user, so they follow the tool messages.
func TestToolResultImagesAreSent(t *testing.T) {
	srv := llmtest.New(t, turn)
	req := llm.Request{Model: "local", MaxTokens: 10, Messages: llmtest.ImageHistory("call_a")}
	if _, err := New("llamacpp", "", srv.URL).Stream(context.Background(), req, func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	msgs := llmtest.Get(srv.Bodies[0], "messages")
	if llmtest.Get(msgs, 2, "role") != "tool" || llmtest.Get(msgs, 3, "role") != "user" ||
		llmtest.Get(msgs, 3, "content", 0, "text") != llm.ImagesIntro || llmtest.Get(msgs, 3, "content", 1, "image_url", "url") != llmtest.PNGDataURL {
		t.Fatalf("messages %v", msgs)
	}
	// A result without images adds no user message.
	srv = llmtest.New(t, turn)
	h := llmtest.ImageHistory("call_a")
	h[2].Results[0].Images = nil
	if _, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "local", Messages: h}, func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	if n := len(llmtest.Get(srv.Bodies[0], "messages").([]any)); n != 3 {
		t.Fatalf("%d messages", n)
	}
}

// A malformed call is replayed with empty arguments, so the server does not fail on it.
func TestMalformedArgumentsAreReplayedAsAnEmptyObject(t *testing.T) {
	srv := llmtest.New(t, turn)
	h := []llm.Message{
		{Role: llm.User, Text: "hi"},
		{Role: llm.Assistant, Calls: []llm.ToolCall{{ID: "call_a", Name: "edit", Arguments: "{\"old_string\":\"a\tb\"}"}}},
		{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "call_a", Content: "invalid arguments", IsError: true}}},
	}
	if _, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "local", Messages: h}, func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	if got := llmtest.Get(srv.Bodies[0], "messages", 1, "tool_calls", 0, "function", "arguments"); got != "{}" {
		t.Fatalf("arguments %v", got)
	}
}

// llama-server sends an SSE comment as a keep-alive while it reads a long prompt. openai-go
// before v3.51.0 dispatched it as an empty event and failed with "unexpected end of JSON input".
func TestAKeepAliveCommentIsIgnored(t *testing.T) {
	srv := llmtest.New(t, ":\n\n"+turn)
	resp, err := New("llamacpp", "", srv.URL).Stream(context.Background(), llm.Request{Model: "local", Messages: []llm.Message{{Role: llm.User, Text: "hi"}}}, func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Text != "Hi" {
		t.Fatalf("text %q", resp.Message.Text)
	}
}
