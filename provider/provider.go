// Package provider is the registry of providers gilda knows, each bound to its vendor's SDK.
package provider

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/anthropic"
	"github.com/shakfu/gilda/llm/compat"
	"github.com/shakfu/gilda/llm/openai"
	"github.com/shakfu/gilda/llm/openrouter"
)

type Entry struct {
	ID string
	// KeyEnv names the variables that hold the key, first set wins. A local server's key is
	// optional.
	KeyEnv []string
	// local marks a server gilda never chooses by itself and never prices.
	local bool
	// BaseURL is the default endpoint; empty uses the SDK's own.
	BaseURL string
	// BaseEnv overrides BaseURL.
	BaseEnv string
	// Model is used when none is given or remembered. Empty takes the endpoint's first model.
	Model string
	// Codec saves the adapter's Native payloads; nil when it keeps none.
	Codec llm.NativeCodec
	open  func(name, key, baseURL string) llm.Provider
}

// Local reports a self-hosted server. Those are never chosen automatically, so an endpoint that
// is merely unreachable is never picked silently, and they bill nothing.
func (e Entry) Local() bool { return e.local }

// Key returns the first key variable that is set.
func (e Entry) Key() string {
	for _, v := range e.KeyEnv {
		if k := os.Getenv(v); k != "" {
			return k
		}
	}
	return ""
}

// URL returns the endpoint after the environment override.
func (e Entry) URL() string {
	if e.BaseEnv != "" {
		if u := os.Getenv(e.BaseEnv); u != "" {
			return u
		}
	}
	return e.BaseURL
}

// Registry is listed in autoselect order: native SDKs first.
var Registry = []Entry{
	{
		ID:     "anthropic",
		KeyEnv: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		Model:  "claude-opus-5",
		Codec:  anthropic.Codec,
		open: func(n, k, u string) llm.Provider {
			return anthropic.New(n, k, u)
		},
	},
	{
		ID:     "openai",
		KeyEnv: []string{"OPENAI_API_KEY"},
		Model:  "gpt-5.5",
		Codec:  openai.Codec,
		open: func(n, k, u string) llm.Provider {
			return openai.New(n, k, u)
		},
	},
	{
		ID:     "openrouter",
		KeyEnv: []string{"OPENROUTER_API_KEY"},
		Model:  "anthropic/claude-opus-5",
		Codec:  openrouter.Codec,
		open: func(n, k, u string) llm.Provider {
			return openrouter.New(n, k, u)
		},
	},
	{
		ID:      "llamacpp",
		BaseURL: "http://localhost:8080/v1",
		BaseEnv: "LLAMACPP_BASE_URL",
		local:   true,
		open:    openCompat,
	},
	{
		ID:      "ollama",
		BaseURL: "http://localhost:11434/v1",
		BaseEnv: "OLLAMA_BASE_URL",
		local:   true,
		open:    openCompat,
	},
	// compat is any other OpenAI-compatible Chat Completions server: LM Studio, vLLM,
	// llama-swap, a machine on the LAN. Its endpoint must be given; the key is optional.
	{
		ID:      "compat",
		KeyEnv:  []string{"COMPAT_API_KEY"},
		BaseEnv: "COMPAT_BASE_URL",
		local:   true,
		open:    openCompat,
	},
}

func openCompat(n, k, u string) llm.Provider { return compat.New(n, k, u) }

// IDs lists the registry ids.
func IDs() []string {
	out := make([]string, len(Registry))
	for i, e := range Registry {
		out[i] = e.ID
	}
	return out
}

func Find(id string) (Entry, bool) {
	i := slices.IndexFunc(Registry, func(e Entry) bool { return e.ID == id })
	if i < 0 {
		return Entry{}, false
	}
	return Registry[i], true
}

// Open builds a provider. key and baseURL override the entry's own when non-empty.
func Open(id, key, baseURL string) (llm.Provider, error) {
	e, ok := Find(id)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q; known: %s", id, strings.Join(IDs(), ", "))
	}
	if key == "" {
		key = e.Key()
	}
	if baseURL == "" {
		baseURL = e.URL()
	}
	if key == "" && !e.Local() && id != "anthropic" {
		return nil, fmt.Errorf("provider %s needs %s", id, strings.Join(e.KeyEnv, " or "))
	}
	if baseURL == "" && e.Local() {
		return nil, fmt.Errorf("provider %s needs --base-url or %s", id, e.BaseEnv)
	}
	return e.open(id, key, baseURL), nil
}

// Choose picks a provider when none is named: the last one used while it can still run, then
// the first cloud entry with a key. keys holds keys given directly, by provider id, and wins
// over the environment.
func Choose(last string, keys map[string]string) (string, error) {
	has := func(e Entry) bool { return keys[e.ID] != "" || e.Key() != "" }
	if e, ok := Find(last); ok && (e.Local() || has(e)) {
		return last, nil
	}
	for _, e := range Registry {
		if !e.Local() && has(e) {
			return e.ID, nil
		}
	}
	return "", fmt.Errorf("no provider key set: export one of ANTHROPIC_API_KEY, OPENAI_API_KEY, " +
		"OPENROUTER_API_KEY, or pass -P llamacpp, ollama or compat for a local server")
}
