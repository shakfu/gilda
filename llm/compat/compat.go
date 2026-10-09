// Package compat speaks OpenAI Chat Completions through openai-go, for local and self-hosted
// servers such as llama.cpp's llama-server and ollama.
package compat

import (
	"context"
	"encoding/json"
	"errors"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/shakfu/gilda/llm"
)

type Provider struct {
	name   string
	client sdk.Client
}

func New(name, key, baseURL string, opts ...option.RequestOption) *Provider {
	// The SDK refuses to build a request without a key; local servers ignore it.
	if key == "" {
		key = "none"
	}
	opts = append([]option.RequestOption{option.WithHTTPClient(llm.HTTPClient)}, opts...)
	opts = append(opts, option.WithAPIKey(key), option.WithBaseURL(baseURL), option.WithMaxRetries(2))
	return &Provider{name: name, client: sdk.NewClient(opts...)}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	stream := p.client.Chat.Completions.NewStreaming(ctx, params(req))
	defer stream.Close()

	var acc sdk.ChatCompletionAccumulator
	seen := map[int64]bool{}
	for stream.Next() {
		chunk := stream.Current()
		if !acc.AddChunk(chunk) {
			return llm.Response{}, errors.New("stream chunk does not continue the response")
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			emit(llm.Event{Kind: llm.TextDelta, Text: d.Content})
		}
		if d.Refusal != "" {
			emit(llm.Event{Kind: llm.TextDelta, Text: d.Refusal})
		}
		// llama-server and ollama stream a thinking model's reasoning outside the spec.
		for _, key := range []string{"reasoning_content", "reasoning"} {
			if f, ok := d.JSON.ExtraFields[key]; ok {
				if s := unquote(f.Raw()); s != "" {
					emit(llm.Event{Kind: llm.ReasoningDelta, Text: s})
				}
			}
		}
		for _, tc := range d.ToolCalls {
			if !seen[tc.Index] && tc.Function.Name != "" {
				seen[tc.Index] = true
				emit(llm.Event{Kind: llm.ToolStart, Text: tc.Function.Name})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, err
	}
	if len(acc.Choices) == 0 || acc.Choices[0].FinishReason == "" {
		return llm.Response{}, llm.Incomplete(ctx)
	}

	choice := acc.Choices[0]
	msg := llm.Message{Role: llm.Assistant, Text: choice.Message.Content + choice.Message.Refusal}
	for _, tc := range choice.Message.ToolCalls {
		msg.Calls = append(msg.Calls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	stop := llm.StopEnd
	// A cut-off or filtered response can carry a call with truncated arguments, so either wins.
	switch {
	case choice.FinishReason == "length":
		stop = llm.StopMaxTokens
	case choice.FinishReason == "content_filter" || choice.Message.Refusal != "":
		stop = llm.StopRefusal
	case len(msg.Calls) > 0:
		stop = llm.StopToolUse
	}
	u := acc.Usage
	return llm.Response{
		Message: msg,
		Usage: llm.Usage{
			Input:     u.PromptTokens,
			Output:    u.CompletionTokens,
			CacheRead: u.PromptTokensDetails.CachedTokens,
		},
		Stop: stop,
	}, nil
}

func params(req llm.Request) sdk.ChatCompletionNewParams {
	var msgs []sdk.ChatCompletionMessageParamUnion
	if req.System != "" {
		msgs = append(msgs, sdk.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.User:
			msgs = append(msgs, sdk.UserMessage(m.Text))
		case llm.Tool:
			parts := []sdk.ChatCompletionContentPartUnionParam{sdk.TextContentPart(llm.ImagesIntro)}
			for _, r := range m.Results {
				msgs = append(msgs, sdk.ToolMessage(r.Content, r.CallID))
				for _, img := range r.Images {
					parts = append(parts, sdk.ImageContentPart(sdk.ChatCompletionContentPartImageImageURLParam{URL: img.DataURL()}))
				}
			}
			// Chat Completions takes images only in user messages, so they follow the results.
			if len(parts) > 1 {
				msgs = append(msgs, sdk.UserMessage(parts))
			}
		case llm.Assistant:
			a := sdk.ChatCompletionAssistantMessageParam{}
			if m.Text != "" {
				a.Content.OfString = sdk.String(m.Text)
			}
			for _, c := range m.Calls {
				a.ToolCalls = append(a.ToolCalls, sdk.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
						ID: c.ID,
						Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      c.Name,
							Arguments: llm.Args(c.Arguments),
						},
					},
				})
			}
			msgs = append(msgs, sdk.ChatCompletionMessageParamUnion{OfAssistant: &a})
		}
	}
	params := sdk.ChatCompletionNewParams{
		Model:         shared.ChatModel(req.Model),
		Messages:      msgs,
		MaxTokens:     sdk.Int(req.MaxTokens),
		StreamOptions: sdk.ChatCompletionStreamOptionsParam{IncludeUsage: sdk.Bool(true)},
	}
	if req.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.Effort)
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, sdk.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			Parameters:  t.Schema,
		}))
	}
	return params
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	var out []llm.Model
	iter := p.client.Models.ListAutoPaging(ctx)
	for iter.Next() {
		m := iter.Current()
		out = append(out, llm.Model{ID: m.ID, Context: nCtx(m.JSON.ExtraFields["meta"].Raw())})
	}
	return out, iter.Err()
}

// nCtx reads llama-server's meta.n_ctx: the window the server was started with, not the one
// the model was trained for.
func nCtx(raw string) int64 {
	var meta struct {
		NCtx int64 `json:"n_ctx"`
	}
	if json.Unmarshal([]byte(raw), &meta) != nil {
		return 0
	}
	return meta.NCtx
}

// unquote decodes a JSON string field; anything else yields "".
func unquote(raw string) string {
	var s string
	if json.Unmarshal([]byte(raw), &s) != nil {
		return ""
	}
	return s
}
