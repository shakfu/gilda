// Package llm holds the provider-independent conversation model and the Provider interface.
//
// History is neutral, but each assistant message also keeps the provider's own payload in
// Native: Anthropic thinking blocks with signatures, OpenAI Responses reasoning items with
// encrypted content, OpenRouter reasoning_details. A provider replays Native when it produced
// it for the same model, so nothing is lost in translation; after a switch it rebuilds the
// message from the neutral fields and the reasoning is dropped.
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
)

type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
	// Tool carries every result for one assistant message. Anthropic requires them in a single
	// user message; splitting them teaches the model to stop calling tools in parallel.
	Tool Role = "tool"
)

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments is the raw JSON object the model streamed.
	Arguments string `json:"arguments"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
	// Images follow Content, for a model that accepts images; see Request.
	Images []Image `json:"images,omitempty"`
}

// ImagesIntro introduces the user message that carries tool results' images for an API that
// takes images only from the user.
const ImagesIntro = "Images returned by the tool calls above, in order:"

// Image is a picture a tool returned.
type Image struct {
	// MediaType is image/png, image/jpeg, image/gif or image/webp.
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
}

// DataURL encodes the image as a data: URL, as chat APIs take it.
func (i Image) DataURL() string {
	return "data:" + i.MediaType + ";base64," + base64.StdEncoding.EncodeToString(i.Data)
}

// Base64 encodes the image's data.
func (i Image) Base64() string { return base64.StdEncoding.EncodeToString(i.Data) }

type Message struct {
	Role    Role         `json:"role"`
	Text    string       `json:"text,omitempty"`
	Calls   []ToolCall   `json:"calls,omitempty"`
	Results []ToolResult `json:"results,omitempty"`
	Native  *Native      `json:"-"`
}

// Native is a provider's own representation of an assistant message.
type Native struct {
	Provider string
	Model    string
	Data     any
}

// NativeCodec saves and restores the Native payloads of one adapter, so a resumed session
// replays its reasoning.
type NativeCodec interface {
	Encode(data any) (json.RawMessage, error)
	Decode(raw json.RawMessage) (any, error)
}

// NativeFor returns the payload when it was produced by this provider and model.
func (m Message) NativeFor(provider, model string) (any, bool) {
	if m.Native == nil || m.Native.Provider != provider || m.Native.Model != model {
		return nil, false
	}
	return m.Native.Data, true
}

type ToolSpec struct {
	Name        string
	Description string
	// Schema is a JSON Schema object with "properties" and "required".
	Schema map[string]any
}

// Properties and Required split Schema for SDKs that take them separately.
func (t ToolSpec) Properties() map[string]any {
	p, _ := t.Schema["properties"].(map[string]any)
	return p
}

// A schema written in Go holds []string; one decoded from JSON holds []any.
func (t ToolSpec) Required() []string {
	switch r := t.Schema["required"].(type) {
	case []string:
		return r
	case []any:
		out := make([]string, 0, len(r))
		for _, v := range r {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Request is one round-trip. Tool results carry images only when the model accepts them; the
// agent strips them otherwise.
type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolSpec
	// MaxTokens caps one response.
	MaxTokens int64
	// Effort is "", "low", "medium", "high", "xhigh" or "max". Empty leaves the model default.
	Effort string
	// SessionID keys prompt caching and sticky routing where the provider supports it.
	SessionID string
}

type Usage struct {
	// Input counts every prompt token, cached or not.
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	// Cost is USD, nil when unknown. Estimated is set when it comes from a price list.
	Cost      *float64 `json:"cost"`
	Estimated bool     `json:"cost_estimated"`
}

// Add accumulates u into s. A cost stays nil until some part reports one.
func (s *Usage) Add(u Usage) {
	s.Input += u.Input
	s.Output += u.Output
	s.CacheRead += u.CacheRead
	s.CacheWrite += u.CacheWrite
	if u.Cost != nil {
		c := *u.Cost
		if s.Cost != nil {
			c += *s.Cost
		}
		s.Cost = &c
	}
	s.Estimated = s.Estimated || u.Estimated
}

type StopReason string

const (
	StopEnd       StopReason = "end"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
)

type Response struct {
	Message Message
	Usage   Usage
	Stop    StopReason
}

type EventKind int

const (
	TextDelta EventKind = iota
	ReasoningDelta
	// ToolStart fires when the model opens a call, before its arguments stream.
	ToolStart
)

type Event struct {
	Kind EventKind
	Text string
}

type Model struct {
	ID      string
	Context int64
}

type Provider interface {
	// Name is the registry id, such as "anthropic".
	Name() string
	// Stream sends one request and reports deltas to emit as they arrive. The returned
	// message is complete only when err is nil.
	Stream(ctx context.Context, req Request, emit func(Event)) (Response, error)
	Models(ctx context.Context) ([]Model, error)
}

// ErrContext means the request exceeds the model's context window.
var ErrContext = errors.New("request exceeds the model's context window")

// Args returns a call's arguments for replay: verbatim when they are a JSON object, else "{}".
// Arguments that were cut off or malformed fail every later request on servers that parse them.
func Args(s string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &obj) != nil {
		return "{}"
	}
	return s
}

// Float returns a pointer to f.
func Float(f float64) *float64 { return &f }
