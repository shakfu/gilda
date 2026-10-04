// Package openai speaks the Responses API through openai-go.
//
// Requests are stateless (store=false). Reasoning survives the tool calls of a turn only as
// encrypted content the client replays, so it is requested with every call and kept in Native.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/shakfu/gilda/llm"
)

type Provider struct {
	name   string
	client sdk.Client
}

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
	params, err := p.params(req)
	if err != nil {
		return llm.Response{}, err
	}
	stream := p.client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	var final *responses.Response
	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta", "response.refusal.delta":
			emit(llm.Event{Kind: llm.TextDelta, Text: ev.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			emit(llm.Event{Kind: llm.ReasoningDelta, Text: ev.Delta})
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				emit(llm.Event{Kind: llm.ToolStart, Text: ev.Item.Name})
			}
		case "response.completed", "response.incomplete":
			r := ev.Response
			final = &r
		case "response.failed":
			return llm.Response{}, fmt.Errorf("response failed: %s", ev.Response.Error.Message)
		case "error":
			return llm.Response{}, fmt.Errorf("stream error: %s", ev.Message)
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, wrap(err)
	}
	if final == nil {
		return llm.Response{}, llm.Incomplete(ctx)
	}
	return p.response(req.Model, final)
}

func (p *Provider) params(req llm.Request) (responses.ResponseNewParams, error) {
	input, err := p.input(req)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(req.Model),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store:           sdk.Bool(false),
		MaxOutputTokens: sdk.Int(req.MaxTokens),
		Include:         []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	}
	if req.System != "" {
		params.Instructions = sdk.String(req.System)
	}
	if req.SessionID != "" {
		params.PromptCacheKey = sdk.String(req.SessionID)
	}
	if req.Effort != "" {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(req.Effort)}
	}
	for _, t := range req.Tools {
		fn := responses.ToolParamOfFunction(t.Name, t.Schema, false)
		fn.OfFunction.Description = sdk.String(t.Description)
		params.Tools = append(params.Tools, fn)
	}
	return params, nil
}

func (p *Provider) input(req llm.Request) (responses.ResponseInputParam, error) {
	var out responses.ResponseInputParam
	for _, m := range req.Messages {
		switch m.Role {
		case llm.User:
			out = append(out, responses.ResponseInputItemParamOfMessage(m.Text, responses.EasyInputMessageRoleUser))
		case llm.Tool:
			for _, r := range m.Results {
				out = append(out, functionOutput(r))
			}
		case llm.Assistant:
			// A payload of another type, from an adapter sharing this name, is rebuilt instead.
			if native, ok := m.NativeFor(p.name, req.Model); ok {
				if items, ok := native.(responses.ResponseInputParam); ok {
					out = append(out, items...)
					continue
				}
			}
			if m.Text != "" {
				out = append(out, responses.ResponseInputItemParamOfMessage(m.Text, responses.EasyInputMessageRoleAssistant))
			}
			for _, c := range m.Calls {
				out = append(out, responses.ResponseInputItemParamOfFunctionCall(c.Arguments, c.ID, c.Name))
			}
		}
	}
	return out, nil
}

// functionOutput is the result's text, followed by its images when it has any.
func functionOutput(r llm.ToolResult) responses.ResponseInputItemUnionParam {
	if len(r.Images) == 0 {
		return responses.ResponseInputItemParamOfFunctionCallOutput(r.CallID, r.Content)
	}
	items := responses.ResponseFunctionCallOutputItemListParam{{OfInputText: &responses.ResponseInputTextContentParam{Text: r.Content}}}
	for _, img := range r.Images {
		items = append(items, responses.ResponseFunctionCallOutputItemUnionParam{
			OfInputImage: &responses.ResponseInputImageContentParam{ImageURL: sdk.String(img.DataURL())},
		})
	}
	return responses.ResponseInputItemParamOfFunctionCallOutput(r.CallID, items)
}

func (p *Provider) response(model string, r *responses.Response) (llm.Response, error) {
	msg := llm.Message{Role: llm.Assistant, Text: r.OutputText()}
	var native responses.ResponseInputParam
	refused := r.Status == responses.ResponseStatusIncomplete && r.IncompleteDetails.Reason == "content_filter"
	for _, item := range r.Output {
		// Output items replay as input items of the same shape: reasoning with its encrypted
		// content, messages, and function calls.
		var in responses.ResponseInputItemUnionParam
		if err := json.Unmarshal([]byte(item.RawJSON()), &in); err != nil {
			return llm.Response{}, fmt.Errorf("replaying %s item: %w", item.Type, err)
		}
		native = append(native, in)
		switch item.Type {
		case "function_call":
			msg.Calls = append(msg.Calls, llm.ToolCall{ID: item.CallID, Name: item.Name, Arguments: item.Arguments.OfString})
		case "message":
			// OutputText skips refusal parts, which would leave the user no reason.
			for _, c := range item.Content {
				if c.Type == "refusal" {
					msg.Text += c.Refusal
					refused = true
				}
			}
		}
	}
	msg.Native = &llm.Native{Provider: p.name, Model: model, Data: native}

	u := r.Usage
	stop := llm.StopEnd
	// A cut-off or filtered response can carry a call with truncated arguments, so either wins.
	switch {
	case r.Status == responses.ResponseStatusIncomplete && r.IncompleteDetails.Reason == "max_output_tokens":
		stop = llm.StopMaxTokens
	case refused:
		stop = llm.StopRefusal
	case len(msg.Calls) > 0:
		stop = llm.StopToolUse
	}
	return llm.Response{
		Message: msg,
		Usage: llm.Usage{
			Input:     u.InputTokens,
			Output:    u.OutputTokens,
			CacheRead: u.InputTokensDetails.CachedTokens,
		},
		Stop: stop,
	}, nil
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	var out []llm.Model
	iter := p.client.Models.ListAutoPaging(ctx)
	for iter.Next() {
		if id := iter.Current().ID; chatModel(id) {
			out = append(out, llm.Model{ID: id})
		}
	}
	return out, iter.Err()
}

// nonChat marks model families that cannot answer a Responses request with tools. OpenAI's
// list carries no capabilities, so this goes by name; a new family may need adding.
var nonChat = []string{
	"babbage", "davinci", "dall-e", "gpt-image", "embedding", "moderation",
	"tts", "whisper", "transcribe", "realtime", "sora",
}

func chatModel(id string) bool {
	for _, s := range nonChat {
		if strings.Contains(id, s) {
			return false
		}
	}
	return true
}

func wrap(err error) error {
	var apierr *sdk.Error
	if errors.As(err, &apierr) && strings.Contains(apierr.Error(), "context_length_exceeded") {
		return errors.Join(llm.ErrContext, err)
	}
	return err
}

// Codec saves and restores the input items this adapter keeps in Native.
var Codec llm.NativeCodec = codec{}

type codec struct{}

func (codec) Encode(data any) (json.RawMessage, error) {
	v, ok := data.(responses.ResponseInputParam)
	if !ok {
		return nil, fmt.Errorf("openai: native payload is %T", data)
	}
	return json.Marshal(v)
}

// Decode refuses anything but a list of typed items: the SDK's decoder accepts other JSON
// without error and yields empty items, which the API would reject.
func (codec) Decode(raw json.RawMessage) (any, error) {
	var items []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Type == "" {
			return nil, errors.New("openai: native payload has an untyped item")
		}
	}
	var v responses.ResponseInputParam
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}
