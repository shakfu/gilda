// Package mock replays a scripted conversation instead of calling a network, for tests and for
// running gilda offline with --mock.
package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/shakfu/gilda/llm"
)

// Step is one scripted provider response.
type Step struct {
	Text      string         `json:"text,omitempty"`
	Reasoning string         `json:"reasoning,omitempty"`
	Calls     []Call         `json:"calls,omitempty"`
	Usage     llm.Usage      `json:"usage"`
	Stop      llm.StopReason `json:"stop,omitempty"`
	Error     string         `json:"error,omitempty"`
	// Incomplete ends the response stream early, as a dropped connection does.
	Incomplete bool `json:"incomplete,omitempty"`
}

type Call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Provider struct {
	mu    sync.Mutex
	steps []Step
	// Requests records every request, for tests.
	Requests []llm.Request
}

func New(steps ...Step) *Provider { return &Provider{steps: steps} }

// Load reads a JSON array of steps.
func Load(path string) (*Provider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var steps []Step
	if err := json.Unmarshal(data, &steps); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return New(steps...), nil
}

func (p *Provider) Name() string { return "mock" }

func (p *Provider) Stream(ctx context.Context, req llm.Request, emit func(llm.Event)) (llm.Response, error) {
	p.mu.Lock()
	p.Requests = append(p.Requests, req)
	if len(p.steps) == 0 {
		p.mu.Unlock()
		return llm.Response{}, errors.New("mock script is exhausted")
	}
	s := p.steps[0]
	p.steps = p.steps[1:]
	n := len(p.Requests)
	p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	if s.Error != "" {
		return llm.Response{}, errors.New(s.Error)
	}
	if s.Incomplete {
		emit(llm.Event{Kind: llm.TextDelta, Text: s.Text})
		return llm.Response{}, llm.ErrIncomplete
	}
	if s.Reasoning != "" {
		emit(llm.Event{Kind: llm.ReasoningDelta, Text: s.Reasoning})
	}
	// Word by word, so a frontend sees a stream.
	for _, w := range strings.SplitAfter(s.Text, " ") {
		if w != "" {
			emit(llm.Event{Kind: llm.TextDelta, Text: w})
		}
	}
	msg := llm.Message{Role: llm.Assistant, Text: s.Text}
	for i, c := range s.Calls {
		emit(llm.Event{Kind: llm.ToolStart, Text: c.Name})
		args := string(c.Arguments)
		if args == "" {
			args = "{}"
		}
		msg.Calls = append(msg.Calls, llm.ToolCall{ID: fmt.Sprintf("mock_%d_%d", n, i), Name: c.Name, Arguments: args})
	}
	stop := s.Stop
	if stop == "" {
		stop = llm.StopEnd
		if len(msg.Calls) > 0 {
			stop = llm.StopToolUse
		}
	}
	return llm.Response{Message: msg, Usage: s.Usage, Stop: stop}, nil
}

func (p *Provider) Models(context.Context) ([]llm.Model, error) {
	return []llm.Model{{ID: "mock", Context: 200_000}}, nil
}
