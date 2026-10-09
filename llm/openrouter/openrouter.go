// Package openrouter speaks OpenRouter's chat API through its Go SDK.
//
// OpenRouter reports the cost of each request, so its usage needs no price list. It also
// returns reasoning_details, which are replayed on the same model so a reasoning model keeps
// its chain of thought across the tool calls of a turn.
package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	sdk "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/models/operations"
	"github.com/OpenRouterTeam/go-sdk/models/sdkerrors"
	"github.com/OpenRouterTeam/go-sdk/optionalnullable"
	"github.com/OpenRouterTeam/go-sdk/retry"

	"github.com/shakfu/gilda/llm"
)

type Provider struct {
	name   string
	client *sdk.OpenRouter
}

func New(name, key, baseURL string) *Provider {
	opts := []sdk.SDKOption{
		sdk.WithSecurity(key),
		sdk.WithXTitle("gilda"),
		sdk.WithClient(streamClient),
		// The SDK's default backs off on 5xx for up to an hour, which would hang a -p run.
		sdk.WithRetryConfig(retry.Config{
			Strategy: "backoff",
			Backoff: &retry.BackoffStrategy{
				InitialInterval: 500, MaxInterval: 10_000, Exponent: 2, MaxElapsedTime: 60_000,
			},
			RetryConnectionErrors: true,
		}),
	}
	if baseURL != "" {
		opts = append(opts, sdk.WithServerURL(baseURL))
	}
	return &Provider{name: name, client: sdk.New(opts...)}
}

// streamClient bounds connecting and waiting for headers but not the response. The SDK's
// default client times out after 60 s in total, which cut every stream that ran longer, and
// its event reader drops the read error, so the cut looked like a stream that just ended.
var streamClient = &http.Client{
	Transport: llm.Transport(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}),
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	res, err := p.client.Chat.Send(ctx, p.request(req), nil,
		operations.WithAcceptHeaderOverride(operations.AcceptHeaderEnumTextEventStream))
	if err != nil {
		return llm.Response{}, wrap(err)
	}
	if res.EventStream == nil {
		return llm.Response{}, errors.New("openrouter returned no event stream")
	}
	events := res.EventStream
	defer events.Close()

	var acc accumulator
	for events.Next() {
		chunk := events.Value().Data
		if chunk.Error != nil {
			return llm.Response{}, fmt.Errorf("openrouter: %s (code %d)", chunk.Error.Message, chunk.Error.Code)
		}
		acc.add(chunk, emit)
	}
	if err := events.Err(); err != nil {
		return llm.Response{}, err
	}
	// The SDK's reader returns no error when the connection drops, so a cancel shows here.
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	if acc.finish == "" {
		return llm.Response{}, llm.Incomplete(ctx)
	}
	return acc.response(p.name, req.Model), nil
}

func (p *Provider) request(req llm.Request) components.ChatRequest {
	var msgs []components.ChatMessages
	if req.System != "" {
		msgs = append(msgs, components.CreateChatMessagesSystem(components.ChatSystemMessage{
			Content: components.CreateChatSystemMessageContentStr(req.System),
			Role:    components.ChatSystemMessageRoleSystem,
		}))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.User:
			msgs = append(msgs, components.CreateChatMessagesUser(components.ChatUserMessage{
				Content: components.CreateChatUserMessageContentStr(m.Text),
				Role:    components.ChatUserMessageRoleUser,
			}))
		case llm.Tool:
			var images []components.ChatContentItems
			for _, r := range m.Results {
				msgs = append(msgs, components.CreateChatMessagesTool(components.ChatToolMessage{
					Content:    components.CreateChatToolMessageContentStr(r.Content),
					Role:       components.ChatToolMessageRoleTool,
					ToolCallID: r.CallID,
				}))
				for _, img := range r.Images {
					images = append(images, components.CreateChatContentItemsImageURL(components.ChatContentImage{
						ImageURL: components.ChatContentImageImageURL{URL: img.DataURL()},
					}))
				}
			}
			// Chat Completions takes images only in user messages, so they follow the results.
			if len(images) > 0 {
				text := components.CreateChatContentItemsText(components.ChatContentText{Text: llm.ImagesIntro})
				msgs = append(msgs, components.CreateChatMessagesUser(components.ChatUserMessage{
					Content: components.CreateChatUserMessageContentArrayOfChatContentItems(append([]components.ChatContentItems{text}, images...)),
					Role:    components.ChatUserMessageRoleUser,
				}))
			}
		case llm.Assistant:
			a := components.ChatAssistantMessage{Role: components.ChatAssistantMessageRoleAssistant}
			if m.Text != "" {
				content := components.CreateChatAssistantMessageContentStr(m.Text)
				a.Content = optionalnullable.From(&content)
			}
			for _, c := range m.Calls {
				a.ToolCalls = append(a.ToolCalls, components.ChatToolCall{
					ID:       c.ID,
					Type:     components.ChatToolCallTypeFunction,
					Function: components.ChatToolCallFunction{Name: c.Name, Arguments: llm.Args(c.Arguments)},
				})
			}
			if native, ok := m.NativeFor(p.name, req.Model); ok {
				// A payload of another type, from an adapter sharing this name, is dropped.
				a.ReasoningDetails, _ = native.([]components.ReasoningDetailUnion)
			}
			msgs = append(msgs, components.CreateChatMessagesAssistant(a))
		}
	}

	stream := true
	include := true
	r := components.ChatRequest{
		Model:     &req.Model,
		Messages:  msgs,
		Stream:    &stream,
		MaxTokens: optionalnullable.From(&req.MaxTokens),
		// Opt-in prompt caching for Anthropic models; other models cache on their own.
		CacheControl:  &components.AnthropicCacheControlDirective{Type: components.AnthropicCacheControlDirectiveTypeEphemeral},
		StreamOptions: optionalnullable.From(&components.ChatStreamOptions{IncludeUsage: &include}),
	}
	if req.SessionID != "" {
		// Sticky routing: a cache lives with one upstream provider.
		r.SessionID = &req.SessionID
		r.PromptCacheKey = optionalnullable.From(&req.SessionID)
	}
	if req.Effort != "" {
		effort := components.ChatRequestEffort(req.Effort)
		r.Reasoning = &components.ChatRequestReasoning{Effort: optionalnullable.From(&effort)}
	}
	for _, t := range req.Tools {
		r.Tools = append(r.Tools, components.CreateChatFunctionToolChatFunctionToolFunction(components.ChatFunctionToolFunction{
			Type: components.ChatFunctionToolTypeFunction,
			Function: components.ChatFunctionToolFunctionFunction{
				Name:        t.Name,
				Description: &t.Description,
				Parameters:  t.Schema,
			},
		}))
	}
	return r
}

type partial struct {
	id, name string
	args     strings.Builder
}

type accumulator struct {
	text      strings.Builder
	calls     []*partial
	reasoning []components.ReasoningDetailUnion
	usage     *components.ChatUsage
	finish    components.ChatFinishReasonEnum
	refused   bool
}

func (a *accumulator) add(chunk components.ChatStreamChunk, emit func(llm.Event)) {
	if chunk.Usage != nil {
		a.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil {
			a.finish = *choice.FinishReason
		}
		d := choice.Delta
		if s, ok := d.Content.GetOrZero(); ok && s != "" {
			a.text.WriteString(s)
			emit(llm.Event{Kind: llm.TextDelta, Text: s})
		}
		if s, ok := d.Refusal.GetOrZero(); ok && s != "" {
			a.text.WriteString(s)
			a.refused = true
			emit(llm.Event{Kind: llm.TextDelta, Text: s})
		}
		if s, ok := d.Reasoning.GetOrZero(); ok && s != "" {
			emit(llm.Event{Kind: llm.ReasoningDelta, Text: s})
		}
		for _, r := range d.ReasoningDetails {
			a.reasoning = mergeReasoning(a.reasoning, r)
		}
		for _, tc := range d.ToolCalls {
			// The index comes from the server; a negative one would panic below.
			if tc.Index < 0 {
				continue
			}
			for int(tc.Index) >= len(a.calls) {
				a.calls = append(a.calls, &partial{})
			}
			c := a.calls[tc.Index]
			if tc.ID != nil {
				c.id = *tc.ID
			}
			if tc.Function == nil {
				continue
			}
			if tc.Function.Name != nil && c.name == "" {
				c.name = *tc.Function.Name
				emit(llm.Event{Kind: llm.ToolStart, Text: c.name})
			}
			if tc.Function.Arguments != nil {
				c.args.WriteString(*tc.Function.Arguments)
			}
		}
	}
}

// mergeReasoning folds a streamed reasoning fragment into the entry it continues: same type,
// same index. Replaying one entry per token would send the reasoning back shredded.
func mergeReasoning(list []components.ReasoningDetailUnion, r components.ReasoningDetailUnion) []components.ReasoningDetailUnion {
	if n := len(list); n > 0 {
		last := &list[n-1]
		switch {
		// A signature closes an entry, so a later fragment starts a new one even without an index.
		case r.ReasoningDetailText != nil && last.ReasoningDetailText != nil &&
			sameIndex(r.ReasoningDetailText.Index, last.ReasoningDetailText.Index) &&
			!signed(last.ReasoningDetailText):
			dst, src := last.ReasoningDetailText, r.ReasoningDetailText
			if t, ok := src.Text.GetOrZero(); ok {
				old, _ := dst.Text.GetOrZero()
				joined := old + t
				dst.Text = optionalnullable.From(&joined)
			}
			if sig, ok := src.Signature.GetOrZero(); ok && sig != "" {
				dst.Signature = src.Signature
			}
			return list
		case r.ReasoningDetailSummary != nil && last.ReasoningDetailSummary != nil &&
			sameIndex(r.ReasoningDetailSummary.Index, last.ReasoningDetailSummary.Index):
			last.ReasoningDetailSummary.Summary += r.ReasoningDetailSummary.Summary
			return list
		}
	}
	return append(list, r)
}

func signed(d *components.ReasoningDetailText) bool {
	sig, ok := d.Signature.GetOrZero()
	return ok && sig != ""
}

func sameIndex(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (a *accumulator) response(provider, model string) llm.Response {
	msg := llm.Message{Role: llm.Assistant, Text: a.text.String()}
	for _, c := range a.calls {
		if c.name != "" {
			msg.Calls = append(msg.Calls, llm.ToolCall{ID: c.id, Name: c.name, Arguments: c.args.String()})
		}
	}
	if len(a.reasoning) > 0 {
		msg.Native = &llm.Native{Provider: provider, Model: model, Data: a.reasoning}
	}
	var usage llm.Usage
	if u := a.usage; u != nil {
		usage.Input = u.PromptTokens
		usage.Output = u.CompletionTokens
		if d, ok := u.PromptTokensDetails.GetOrZero(); ok {
			usage.CacheRead = deref(d.CachedTokens)
			usage.CacheWrite = deref(d.CacheWriteTokens)
		}
		if c, ok := u.Cost.Get(); ok && c != nil {
			usage.Cost = llm.Float(*c)
		}
	}
	stop := llm.StopEnd
	// A cut-off or filtered response can carry a call with truncated arguments, so either wins.
	switch {
	case a.finish == components.ChatFinishReasonEnumLength:
		stop = llm.StopMaxTokens
	case a.finish == components.ChatFinishReasonEnumContentFilter || a.refused:
		stop = llm.StopRefusal
	case len(msg.Calls) > 0:
		stop = llm.StopToolUse
	}
	return llm.Response{Message: msg, Usage: usage, Stop: stop}
}

func (p *Provider) Models(ctx context.Context) ([]llm.Model, error) {
	models, err := List(ctx, p.client)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Model, 0, len(models))
	for _, m := range models {
		out = append(out, llm.Model{ID: m.ID, Context: deref(m.ContextLength)})
	}
	return out, nil
}

// List returns OpenRouter's public model list. It needs no key.
func List(ctx context.Context, client *sdk.OpenRouter) ([]components.Model, error) {
	if client == nil {
		client = sdk.New()
	}
	res, err := client.Models.List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return res.Result.Data, nil
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func wrap(err error) error {
	var apierr *sdkerrors.APIError
	if errors.As(err, &apierr) && strings.Contains(apierr.Body, "context length") {
		return errors.Join(llm.ErrContext, err)
	}
	return err
}

// Codec saves and restores the reasoning_details this adapter keeps in Native.
var Codec llm.NativeCodec = codec{}

type codec struct{}

func (codec) Encode(data any) (json.RawMessage, error) {
	v, ok := data.([]components.ReasoningDetailUnion)
	if !ok {
		return nil, fmt.Errorf("openrouter: native payload is %T", data)
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
			return nil, errors.New("openrouter: native payload has an untyped item")
		}
	}
	var v []components.ReasoningDetailUnion
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}
