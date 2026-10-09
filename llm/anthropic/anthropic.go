// Package anthropic speaks the Messages API through anthropic-sdk-go.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/shakfu/gilda/llm"
)

type Provider struct {
	name   string
	client sdk.Client
}

// New builds a client. An empty key lets the SDK resolve credentials itself: ANTHROPIC_API_KEY,
// ANTHROPIC_AUTH_TOKEN, or an `ant auth login` profile.
func New(name, key, baseURL string, opts ...option.RequestOption) *Provider {
	if key != "" {
		opts = append(opts, option.WithAPIKey(key))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	opts = append([]option.RequestOption{option.WithHTTPClient(llm.HTTPClient)}, opts...)
	opts = append(opts, option.WithMaxRetries(4))
	return &Provider{name: name, client: sdk.NewClient(opts...)}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	stream := p.client.Messages.NewStreaming(ctx, p.params(req))
	defer stream.Close()

	var msg sdk.Message
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return llm.Response{}, err
		}
		switch e := ev.AsAny().(type) {
		case sdk.ContentBlockStartEvent:
			if e.ContentBlock.Type == "tool_use" {
				emit(llm.Event{Kind: llm.ToolStart, Text: e.ContentBlock.Name})
			}
		case sdk.ContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case sdk.TextDelta:
				emit(llm.Event{Kind: llm.TextDelta, Text: d.Text})
			case sdk.ThinkingDelta:
				emit(llm.Event{Kind: llm.ReasoningDelta, Text: d.Thinking})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, wrap(err)
	}
	if msg.StopReason == "" {
		// A proxy or a dropped connection can end the stream after complete-looking output.
		return llm.Response{}, llm.Incomplete(ctx)
	}
	return p.response(req.Model, msg), nil
}

func (p *Provider) params(req llm.Request) sdk.MessageNewParams {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: req.MaxTokens,
		Messages:  p.messages(req),
		// Auto-placed on the last cacheable block, so each turn reads the previous turn's prefix.
		CacheControl: sdk.NewCacheControlEphemeralParam(),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{
			Text:         req.System,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}}
	}
	for _, t := range req.Tools {
		tool := sdk.ToolParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			InputSchema: sdk.ToolInputSchemaParam{
				Properties: t.Properties(),
				Required:   t.Required(),
			},
			// Large write inputs stream as generated. Arguments are validated by the tool, which
			// reports invalid JSON as an error result.
			EagerInputStreaming: sdk.Bool(true),
		}
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &tool})
	}
	if req.Effort != "" {
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(req.Effort)}
	}
	return params
}

func (p *Provider) messages(req llm.Request) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case llm.User:
			out = append(out, sdk.NewUserMessage(sdk.NewTextBlock(m.Text)))
		case llm.Tool:
			blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.Results))
			for _, r := range m.Results {
				blocks = append(blocks, toolResult(r))
			}
			out = append(out, sdk.NewUserMessage(blocks...))
		case llm.Assistant:
			// A payload of another type, from an adapter sharing this name, is rebuilt instead.
			if native, ok := m.NativeFor(p.name, req.Model); ok {
				if mp, ok := native.(sdk.MessageParam); ok {
					out = append(out, mp)
					continue
				}
			}
			var blocks []sdk.ContentBlockParamUnion
			if m.Text != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Text))
			}
			for _, c := range m.Calls {
				blocks = append(blocks, sdk.NewToolUseBlock(toolID(c.ID), json.RawMessage(llm.Args(c.Arguments)), c.Name))
			}
			if len(blocks) > 0 {
				out = append(out, sdk.NewAssistantMessage(blocks...))
			}
		}
	}
	return out
}

// toolResult carries the result's images after its text, inside the tool_result block.
func toolResult(r llm.ToolResult) sdk.ContentBlockParamUnion {
	b := sdk.NewToolResultBlock(toolID(r.CallID), r.Content, r.IsError)
	for _, img := range r.Images {
		b.OfToolResult.Content = append(b.OfToolResult.Content, sdk.ToolResultBlockParamContentUnion{
			OfImage: &sdk.ImageBlockParam{Source: sdk.ImageBlockParamSourceUnion{OfBase64: &sdk.Base64ImageSourceParam{
				Data: img.Base64(), MediaType: sdk.Base64ImageSourceMediaType(img.MediaType),
			}}},
		})
	}
	return b
}

func (p *Provider) response(model string, msg sdk.Message) llm.Response {
	out := llm.Message{
		Role:   llm.Assistant,
		Native: &llm.Native{Provider: p.name, Model: model, Data: msg.ToParam()},
	}
	var text strings.Builder
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case sdk.TextBlock:
			text.WriteString(b.Text)
		case sdk.ToolUseBlock:
			out.Calls = append(out.Calls, llm.ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
		}
	}
	out.Text = text.String()

	u := msg.Usage
	usage := llm.Usage{
		// input_tokens excludes the cached parts, which are billed at their own rates.
		Input:      u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
	stop := llm.StopEnd
	switch msg.StopReason {
	case sdk.StopReasonToolUse:
		stop = llm.StopToolUse
	case sdk.StopReasonMaxTokens:
		stop = llm.StopMaxTokens
	case sdk.StopReasonRefusal:
		stop = llm.StopRefusal
	}
	return llm.Response{Message: out, Usage: usage, Stop: stop}
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	var out []llm.Model
	iter := p.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})
	for iter.Next() {
		m := iter.Current()
		out = append(out, llm.Model{ID: m.ID, Context: m.MaxInputTokens})
	}
	return out, iter.Err()
}

// toolID makes another provider's call id valid for Anthropic, which accepts only
// [a-zA-Z0-9_-]. Calls and results map the same way, so they still pair up.
func toolID(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, id)
}

func wrap(err error) error {
	var apierr *sdk.Error
	if errors.As(err, &apierr) && apierr.StatusCode == http.StatusBadRequest &&
		strings.Contains(apierr.Error(), "prompt is too long") {
		return errors.Join(llm.ErrContext, err)
	}
	return err
}

// Codec saves and restores the MessageParam this adapter keeps in Native.
var Codec llm.NativeCodec = codec{}

type codec struct{}

func (codec) Encode(data any) (json.RawMessage, error) {
	v, ok := data.(sdk.MessageParam)
	if !ok {
		return nil, fmt.Errorf("anthropic: native payload is %T", data)
	}
	return json.Marshal(v)
}

// Decode refuses anything but an assistant message with content: the SDK's decoder accepts
// other JSON without error and yields an empty message, which the API would reject.
func (codec) Decode(raw json.RawMessage) (any, error) {
	var v sdk.MessageParam
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if v.Role != sdk.MessageParamRoleAssistant || len(v.Content) == 0 {
		return nil, errors.New("anthropic: native payload is not an assistant message")
	}
	return v, nil
}
