package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OpenRouterTeam/go-sdk/models/components"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/llmtest"
)

var turn = llmtest.SSE(
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"th","reasoning_details":[{"type":"reasoning.text","text":"th","index":0}]},"finish_reason":null}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{"reasoning":"ink","reasoning_details":[{"type":"reasoning.text","text":"ink","signature":"SIG","index":0}]},"finish_reason":null}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a\"}"}}]},"finish_reason":null}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	"", `{"id":"g1","object":"chat.completion.chunk","created":1,"model":"anthropic/claude-x","choices":[],"usage":{"prompt_tokens":200,"completion_tokens":30,"total_tokens":230,"cost":0.0042,"prompt_tokens_details":{"cached_tokens":150,"cache_write_tokens":20}}}`,
	"", `[DONE]`,
)

var answer = llmtest.SSE(
	"", `{"id":"g2","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`,
	"", `[DONE]`,
)

func request(model string, history ...llm.Message) llm.Request {
	return llm.Request{Model: model, System: "sys", Messages: history, MaxTokens: 100, SessionID: "sess", Effort: "medium",
		Tools: []llm.ToolSpec{{Name: "read", Description: "d", Schema: map[string]any{"type": "object", "properties": map[string]any{}}}}}
}

func TestStreamReportsCostAndMergesReasoning(t *testing.T) {
	srv := llmtest.New(t, turn, answer)
	p := New("openrouter", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("anthropic/claude-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	m := resp.Message
	if m.Text != "Hi" || len(m.Calls) != 1 || m.Calls[0].Arguments != `{"path":"a"}` || resp.Stop != llm.StopToolUse {
		t.Fatalf("message %+v", m)
	}
	u := resp.Usage
	if u.Cost == nil || *u.Cost != 0.0042 || u.Estimated || u.CacheRead != 150 || u.CacheWrite != 20 || u.Input != 200 {
		t.Fatalf("usage %+v", u)
	}

	body := srv.Bodies[0]
	checks := map[string]bool{
		"cache_control":  llmtest.Get(body, "cache_control", "type") == "ephemeral",
		"session_id":     llmtest.Get(body, "session_id") == "sess",
		"stream":         llmtest.Get(body, "stream") == true,
		"include usage":  llmtest.Get(body, "stream_options", "include_usage") == true,
		"effort":         llmtest.Get(body, "reasoning", "effort") == "medium",
		"system message": llmtest.Get(body, "messages", 0, "role") == "system",
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("request: %s (%v)", name, body)
		}
	}

	// Streamed fragments of one reasoning entry replay as one entry, signature included.
	history := []llm.Message{user, m, {Role: llm.Tool, Results: []llm.ToolResult{{CallID: "call_1", Content: "x"}}}}
	if _, err := p.Stream(context.Background(), request("anthropic/claude-x", history...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	details := llmtest.Get(srv.Bodies[1], "messages", 2, "reasoning_details")
	list, _ := details.([]any)
	if len(list) != 1 || llmtest.Get(list, 0, "text") != "think" || llmtest.Get(list, 0, "signature") != "SIG" {
		t.Fatalf("replayed reasoning %v", details)
	}
	if llmtest.Get(srv.Bodies[1], "messages", 3, "tool_call_id") != "call_1" {
		t.Fatalf("tool result %v", llmtest.Get(srv.Bodies[1], "messages", 3))
	}
}

func TestASignatureClosesAReasoningEntry(t *testing.T) {
	var a accumulator
	chunk := func(data string) {
		srvChunk(t, &a, data)
	}
	chunk(`{"id":"g","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"one","signature":"S1"}]}}]}`)
	chunk(`{"id":"g","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"two","signature":"S2"}]}}]}`)
	if len(a.reasoning) != 2 {
		t.Fatalf("merged signed entries: %d", len(a.reasoning))
	}
}

func srvChunk(t *testing.T, a *accumulator, data string) {
	t.Helper()
	var c components.ChatStreamChunk
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	a.add(c, func(llm.Event) {})
}

// A cancel while the stream is open reads as a cancel. The SDK's reader returns no error when
// the connection closes, which read as "stream ended without a finish reason".
func TestACancelMidStreamIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"g\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := New("openrouter", "k", srv.URL).Stream(ctx, request("m", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if streamClient.Timeout != 0 {
		t.Fatal("the streaming client has an overall timeout, which cuts long streams")
	}
}

func TestRetriesAreReported(t *testing.T) {
	srv := llmtest.New(t, llmtest.Status(502, `{"error":{"message":"upstream"}}`), answer)
	var reasons []string
	ctx := llm.WithRetries(context.Background(), func(_ int, r string) { reasons = append(reasons, r) })
	resp, err := New("openrouter", "k", srv.URL).Stream(ctx, request("m", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if err != nil || resp.Message.Text != "done" {
		t.Fatalf("%v %+v", err, resp)
	}
	if len(reasons) != 1 || reasons[0] != "502 Bad Gateway" {
		t.Fatalf("reasons %v", reasons)
	}
}

// A connection dropped mid-stream reports the read error, which the SDK's reader discards.
func TestADroppedConnectionIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"g\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		// Close without the chunked encoding's terminator, as a dropped connection does.
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer srv.Close()
	ctx := llm.WithRetries(context.Background(), func(int, string) {})
	_, err := New("openrouter", "k", srv.URL).Stream(ctx, request("m", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if !errors.Is(err, llm.ErrIncomplete) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v", err)
	}
}

func TestANegativeToolIndexIsIgnored(t *testing.T) {
	var a accumulator
	srvChunk(t, &a, `{"id":"g","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":-1,"id":"x","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`)
	if len(a.calls) != 0 {
		t.Fatalf("calls %d", len(a.calls))
	}
}

// A refusal or a filtered response must reach the agent as a refusal, not as a finished answer.
func TestRefusalsAndFilteredResponsesStopAsRefusals(t *testing.T) {
	const chunk = `{"id":"g","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`
	cases := map[string]struct{ delta, finish, text string }{
		"refusal":        {`{"refusal":"No."}`, `"stop"`, "No."},
		"content filter": {`{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"write","arguments":"{\"pa"}}]}`, `"content_filter"`, ""},
	}
	for name, c := range cases {
		srv := llmtest.New(t, llmtest.SSE("", fmt.Sprintf(chunk, c.delta, c.finish), "", "[DONE]"))
		var streamed strings.Builder
		resp, err := New("openrouter", "k", srv.URL).Stream(context.Background(), request("m", llm.Message{Role: llm.User, Text: "hi"}), func(e llm.Event) {
			if e.Kind == llm.TextDelta {
				streamed.WriteString(e.Text)
			}
		})
		if err != nil || resp.Stop != llm.StopRefusal || resp.Message.Text != c.text || streamed.String() != c.text {
			t.Errorf("%s: stop %q text %q streamed %q err %v", name, resp.Stop, resp.Message.Text, streamed.String(), err)
		}
	}
}

// A saved and restored payload replays the same request as the one kept in memory.
func TestNativePayloadSurvivesSaving(t *testing.T) {
	srv := llmtest.New(t, turn, answer, answer)
	p := New("openrouter", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("anthropic/claude-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	results := llm.Message{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "call_1", Content: "x"}}}
	for _, m := range []llm.Message{resp.Message, llmtest.Restore(t, Codec, resp.Message)} {
		h := []llm.Message{user, m, results}
		if _, err := p.Stream(context.Background(), request("anthropic/claude-x", h...), func(llm.Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(srv.Bodies[1], srv.Bodies[2]) {
		t.Fatalf("in memory:\n%v\nrestored:\n%v", srv.Bodies[1], srv.Bodies[2])
	}
	if _, err := Codec.Encode("not a payload"); err == nil {
		t.Fatal("a foreign payload was encoded")
	}
	for _, bad := range []string{`"x"`, `{}`, `[{}]`} {
		if _, err := Codec.Decode(json.RawMessage(bad)); err == nil {
			t.Errorf("decoded %s", bad)
		}
	}
}

// Chat Completions takes images only from the user, so they follow the tool messages.
func TestToolResultImagesAreSent(t *testing.T) {
	srv := llmtest.New(t, answer)
	if _, err := New("openrouter", "k", srv.URL).Stream(context.Background(), request("m", llmtest.ImageHistory("call_1")...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	msgs := llmtest.Get(srv.Bodies[0], "messages")
	if llmtest.Get(msgs, 3, "role") != "tool" || llmtest.Get(msgs, 3, "content") != "a.png is a PNG image" ||
		llmtest.Get(msgs, 4, "role") != "user" || llmtest.Get(msgs, 4, "content", 0, "text") != llm.ImagesIntro ||
		llmtest.Get(msgs, 4, "content", 1, "image_url", "url") != llmtest.PNGDataURL {
		t.Fatalf("messages %v", msgs)
	}
}

// A payload this adapter did not make, under its name and model, is rebuilt from text and
// calls rather than asserted.
func TestAForeignPayloadIsRebuilt(t *testing.T) {
	srv := llmtest.New(t, answer)
	h := llmtest.ImageHistory("call_1")
	h[1].Text = "looking"
	h[1].Native = &llm.Native{Provider: "openrouter", Model: "m", Data: "not a payload"}
	if _, err := New("openrouter", "k", srv.URL).Stream(context.Background(), request("m", h...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	if body, _ := json.Marshal(srv.Bodies[0]); !strings.Contains(string(body), `"looking"`) {
		t.Fatalf("the message was not rebuilt: %s", body)
	}
}
