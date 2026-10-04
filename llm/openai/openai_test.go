package openai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/llmtest"
)

const completed = `{"type":"response.completed","sequence_number":5,"response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"gpt-x","output":[` +
	`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"},` +
	`{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hi","annotations":[]}]},` +
	`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"a\"}","status":"completed"}` +
	`],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":80},"output_tokens":10,"output_tokens_details":{"reasoning_tokens":5},"total_tokens":110}}}`

var turn = llmtest.SSE(
	"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"plan","sequence_number":1}`,
	"response.output_text.delta", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"Hi","sequence_number":2,"logprobs":[]}`,
	"response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"","status":"in_progress"},"sequence_number":3}`,
	"response.completed", completed,
)

var answer = llmtest.SSE(
	"response.completed", `{"type":"response.completed","sequence_number":1,"response":{"id":"resp_2","object":"response","created_at":1,"status":"completed","model":"gpt-x","output":[{"type":"message","id":"msg_2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`,
)

func request(model string, history ...llm.Message) llm.Request {
	return llm.Request{
		Model: model, System: "be terse", Messages: history, MaxTokens: 1000, Effort: "low", SessionID: "sess",
		Tools: []llm.ToolSpec{{Name: "read", Description: "read a file", Schema: map[string]any{
			"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"},
		}}},
	}
}

func TestStreamAssemblesTheCompletedResponse(t *testing.T) {
	srv := llmtest.New(t, turn)
	p := New("openai", "k", srv.URL)
	var text, reasoning strings.Builder
	var started []string
	resp, err := p.Stream(context.Background(), request("gpt-x", llm.Message{Role: llm.User, Text: "hi"}), func(e llm.Event) {
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
	if text.String() != "Hi" || reasoning.String() != "plan" || len(started) != 1 {
		t.Fatalf("events: %q %q %v", text.String(), reasoning.String(), started)
	}
	m := resp.Message
	if m.Text != "Hi" || len(m.Calls) != 1 || m.Calls[0].ID != "call_1" || m.Calls[0].Arguments != `{"path":"a"}` {
		t.Fatalf("message %+v", m)
	}
	if resp.Stop != llm.StopToolUse || resp.Usage.Input != 100 || resp.Usage.CacheRead != 80 || resp.Usage.Output != 10 {
		t.Fatalf("stop %q usage %+v", resp.Stop, resp.Usage)
	}

	body := srv.Bodies[0]
	checks := map[string]bool{
		"store false":            llmtest.Get(body, "store") == false,
		"encrypted reasoning":    llmtest.Get(body, "include", 0) == "reasoning.encrypted_content",
		"prompt cache key":       llmtest.Get(body, "prompt_cache_key") == "sess",
		"instructions":           llmtest.Get(body, "instructions") == "be terse",
		"effort":                 llmtest.Get(body, "reasoning", "effort") == "low",
		"function tool":          llmtest.Get(body, "tools", 0, "name") == "read",
		"streamed":               llmtest.Get(body, "stream") == true,
		"responses endpoint":     strings.HasSuffix(srv.Paths[0], "/responses"),
		"user message as input":  llmtest.Get(body, "input", 0, "role") == "user",
		"max output tokens sent": llmtest.Get(body, "max_output_tokens") == float64(1000),
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("request: %s", name)
		}
	}
}

// With store=false, reasoning survives the tool calls of a turn only as encrypted content the
// client sends back. Another model gets the text and the call instead.
func TestReasoningReplaysOnlyToTheSameModel(t *testing.T) {
	srv := llmtest.New(t, turn, answer, answer)
	p := New("openai", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("gpt-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{user, resp.Message, {Role: llm.Tool, Results: []llm.ToolResult{{CallID: "call_1", Content: "text"}}}}
	for _, model := range []string{"gpt-x", "gpt-y"} {
		if _, err := p.Stream(context.Background(), request(model, history...), func(llm.Event) {}); err != nil {
			t.Fatal(err)
		}
	}
	same, other := srv.Bodies[1], srv.Bodies[2]
	if llmtest.Get(same, "input", 1, "type") != "reasoning" || llmtest.Get(same, "input", 1, "encrypted_content") != "ENC" {
		t.Fatalf("same model: %v", llmtest.Get(same, "input"))
	}
	if llmtest.Get(same, "input", 3, "type") != "function_call" || llmtest.Get(same, "input", 4, "type") != "function_call_output" {
		t.Fatalf("same model order: %v", llmtest.Get(same, "input"))
	}
	for _, item := range llmtest.Get(other, "input").([]any) {
		if llmtest.Get(item, "type") == "reasoning" {
			t.Fatal("reasoning sent to another model")
		}
	}
	if llmtest.Get(other, "input", 2, "type") != "function_call" || llmtest.Get(other, "input", 2, "call_id") != "call_1" {
		t.Fatalf("other model: %v", llmtest.Get(other, "input"))
	}
}

func TestAStreamWithoutCompletionIsAnError(t *testing.T) {
	srv := llmtest.New(t, llmtest.SSE("response.output_text.delta",
		`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"x","sequence_number":1,"logprobs":[]}`))
	_, err := New("openai", "k", srv.URL).Stream(context.Background(), request("gpt-x", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if err == nil {
		t.Fatal("a cut stream was taken as complete")
	}
}

func TestChatModels(t *testing.T) {
	for id, want := range map[string]bool{
		"gpt-5.5": true, "gpt-6-luna": true, "o4-mini": true, "gpt-4o-mini": true,
		"babbage-002": false, "davinci-002": false, "text-embedding-3-large": false, "tts-1-hd": false,
		"whisper-1": false, "gpt-4o-transcribe": false, "gpt-4o-mini-tts": false, "dall-e-3": false,
		"gpt-image-1": false, "omni-moderation-latest": false, "gpt-realtime": false, "sora-2-pro": false,
	} {
		if chatModel(id) != want {
			t.Errorf("chatModel(%q) = %v", id, !want)
		}
	}
}

func TestRetriesAreReported(t *testing.T) {
	srv := llmtest.New(t, llmtest.Status(503, `{"error":{"message":"busy"}}`), answer)
	var reasons []string
	ctx := llm.WithRetries(context.Background(), func(_ int, r string) { reasons = append(reasons, r) })
	resp, err := New("openai", "k", srv.URL).Stream(ctx, request("gpt-x", llm.Message{Role: llm.User, Text: "hi"}), func(llm.Event) {})
	if err != nil || resp.Message.Text != "done" {
		t.Fatalf("%v %+v", err, resp)
	}
	if len(reasons) != 1 || reasons[0] != "503 Service Unavailable" {
		t.Fatalf("reasons %v", reasons)
	}
}

// A refusal or a filtered response must reach the agent as a refusal, not as a finished answer.
func TestRefusalsAndFilteredResponsesStopAsRefusals(t *testing.T) {
	const head = `{"id":"r","object":"response","created_at":1,"model":"m","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}},`
	call := `{"type":"function_call","id":"fc","call_id":"c","name":"write","arguments":"{\"pa","status":"incomplete"}`
	cases := map[string]struct {
		event, response, text string
		want                  llm.StopReason
	}{
		"refusal": {"response.completed",
			`"status":"completed","output":[{"type":"message","id":"m","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"No."}]}]}`,
			"No.", llm.StopRefusal},
		"content filter": {"response.incomplete",
			`"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[` + call + `]}`,
			"", llm.StopRefusal},
		"max tokens": {"response.incomplete",
			`"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[` + call + `]}`,
			"", llm.StopMaxTokens},
	}
	for name, c := range cases {
		srv := llmtest.New(t, llmtest.SSE(
			"response.refusal.delta", `{"type":"response.refusal.delta","item_id":"m","output_index":0,"content_index":0,"delta":"`+c.text+`","sequence_number":1}`,
			c.event, `{"type":"`+c.event+`","sequence_number":2,"response":`+head+c.response+`}`,
		))
		var streamed strings.Builder
		resp, err := New("openai", "k", srv.URL).Stream(context.Background(), request("m", llm.Message{Role: llm.User, Text: "hi"}), func(e llm.Event) {
			if e.Kind == llm.TextDelta {
				streamed.WriteString(e.Text)
			}
		})
		if err != nil || resp.Stop != c.want || resp.Message.Text != c.text || streamed.String() != c.text {
			t.Errorf("%s: stop %q text %q streamed %q err %v", name, resp.Stop, resp.Message.Text, streamed.String(), err)
		}
	}
}

// A saved and restored payload replays the same request as the one kept in memory.
func TestNativePayloadSurvivesSaving(t *testing.T) {
	srv := llmtest.New(t, turn, answer, answer)
	p := New("openai", "k", srv.URL)
	user := llm.Message{Role: llm.User, Text: "hi"}
	resp, err := p.Stream(context.Background(), request("gpt-x", user), func(llm.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	results := llm.Message{Role: llm.Tool, Results: []llm.ToolResult{{CallID: "call_1", Content: "x"}}}
	for _, m := range []llm.Message{resp.Message, llmtest.Restore(t, Codec, resp.Message)} {
		h := []llm.Message{user, m, results}
		if _, err := p.Stream(context.Background(), request("gpt-x", h...), func(llm.Event) {}); err != nil {
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

// An image goes inside the function_call_output, after its text.
func TestToolResultImagesAreSent(t *testing.T) {
	srv := llmtest.New(t, answer)
	if _, err := New("openai", "k", srv.URL).Stream(context.Background(), request("gpt-x", llmtest.ImageHistory("call_1")...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	item := llmtest.Get(srv.Bodies[0], "input", 2)
	if llmtest.Get(item, "type") != "function_call_output" || llmtest.Get(item, "output", 0, "text") != "a.png is a PNG image" ||
		llmtest.Get(item, "output", 1, "type") != "input_image" || llmtest.Get(item, "output", 1, "image_url") != llmtest.PNGDataURL {
		t.Fatalf("item %v", item)
	}
}

// A payload this adapter did not make, under its name and model, is rebuilt from text and
// calls rather than asserted.
func TestAForeignPayloadIsRebuilt(t *testing.T) {
	srv := llmtest.New(t, answer)
	h := llmtest.ImageHistory("call_1")
	h[1].Text = "looking"
	h[1].Native = &llm.Native{Provider: "openai", Model: "gpt-x", Data: "not a payload"}
	if _, err := New("openai", "k", srv.URL).Stream(context.Background(), request("gpt-x", h...), func(llm.Event) {}); err != nil {
		t.Fatal(err)
	}
	if body, _ := json.Marshal(srv.Bodies[0]); !strings.Contains(string(body), `"looking"`) {
		t.Fatalf("the message was not rebuilt: %s", body)
	}
}
