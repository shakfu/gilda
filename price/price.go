// Package price estimates cost and context windows from OpenRouter's public model list.
//
// OpenAI reports neither a cost nor a context window, and Anthropic no cost. OpenRouter lists
// their models at the vendors' rates and needs no key to do so. A table kept in gilda would go
// stale.
package price

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OpenRouterTeam/go-sdk/models/components"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/openrouter"
	"github.com/shakfu/gilda/state"
)

// USD per token.
type Rates struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type Tier struct {
	// MinPrompt is exclusive: the tier applies above it.
	MinPrompt int64 `json:"min_prompt"`
	Rates     Rates `json:"rates"`
}

type Entry struct {
	Rates   *Rates `json:"rates,omitempty"`
	Tiers   []Tier `json:"tiers,omitempty"`
	Context int64  `json:"context,omitempty"`
	// Inputs lists the input modalities, such as "text" and "image"; nil when unknown, as in a
	// list cached before it was recorded.
	Inputs []string `json:"inputs,omitempty"`
}

// Images reports whether the model accepts images, and whether the list says.
func (e Entry) Images() (accepts, known bool) {
	if e.Inputs == nil {
		return false, false
	}
	return slices.Contains(e.Inputs, "image"), true
}

// Cost prices u, applying the highest long-prompt tier it reaches.
func (e Entry) Cost(u llm.Usage) (float64, bool) {
	if e.Rates == nil {
		return 0, false
	}
	// The matching tier with the highest threshold, whatever order the tiers are in: a
	// listing, or a catalog cached before, need not sort them.
	r, best := *e.Rates, int64(-1)
	for _, t := range e.Tiers {
		if u.Input > t.MinPrompt && t.MinPrompt > best {
			r, best = t.Rates, t.MinPrompt
		}
	}
	fresh := max(u.Input-u.CacheRead-u.CacheWrite, 0)
	return float64(fresh)*r.Prompt + float64(u.CacheRead)*r.CacheRead +
		float64(u.CacheWrite)*r.CacheWrite + float64(u.Output)*r.Completion, true
}

type Catalog struct {
	Fetched time.Time        `json:"fetched"`
	Models  map[string]Entry `json:"models"`
}

const maxAge = 24 * time.Hour

// Load returns the cached catalog, refetching it when it is older than a day. A failed fetch
// falls back to a stale cache; with no cache at all the catalog is empty.
func Load(ctx context.Context, cacheDir string, refresh bool) (*Catalog, error) {
	path := filepath.Join(cacheDir, "openrouter-models.json")
	cached := &Catalog{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, cached)
	}
	if !refresh && time.Since(cached.Fetched) < maxAge && len(cached.Models) > 0 {
		return cached, nil
	}
	models, err := openrouter.List(ctx, nil)
	if err != nil {
		return cached, err
	}
	fresh := FromModels(models)
	if data, err := json.Marshal(fresh); err == nil {
		_ = state.WriteFile(path, data)
	}
	return fresh, nil
}

// FromModels builds a catalog from OpenRouter's listing.
func FromModels(models []components.Model) *Catalog {
	c := &Catalog{Fetched: time.Now(), Models: make(map[string]Entry, len(models))}
	for _, m := range models {
		e := Entry{Inputs: []string{}}
		if m.ContextLength != nil {
			e.Context = *m.ContextLength
		}
		for _, in := range m.Architecture.InputModalities {
			e.Inputs = append(e.Inputs, string(in))
		}
		p := m.Pricing
		base, ok := rates(p.Prompt, p.Completion, p.InputCacheRead, p.InputCacheWrite, nil)
		if ok {
			e.Rates = &base
			for _, o := range p.Overrides {
				// Time-of-day discounts are not modelled; a long-prompt tier is.
				if o.MinPromptTokens == nil || o.UtcStart != nil || len(o.UtcDays) > 0 {
					continue
				}
				r, ok := rates(deref(o.Prompt), deref(o.Completion), o.InputCacheRead, o.InputCacheWrite, &base)
				if ok {
					e.Tiers = append(e.Tiers, Tier{MinPrompt: int64(*o.MinPromptTokens), Rates: r})
				}
			}
		}
		c.Models[m.ID] = e
	}
	return c
}

// rates parses OpenRouter's decimal strings. A negative price marks a model priced per route,
// which has no fixed rate. Missing cache rates fall back to the prompt rate.
func rates(prompt, completion string, read, write *string, fallback *Rates) (Rates, bool) {
	parse := func(s string, def float64, hasDef bool) (float64, bool) {
		if s == "" {
			return def, hasDef
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 {
			return 0, false
		}
		return v, true
	}
	var r Rates
	var ok bool
	fb := fallback != nil
	if fallback == nil {
		fallback = &Rates{}
	}
	if r.Prompt, ok = parse(prompt, fallback.Prompt, fb); !ok {
		return r, false
	}
	if r.Completion, ok = parse(completion, fallback.Completion, fb); !ok {
		return r, false
	}
	cacheDef := r.Prompt
	if fb {
		cacheDef = fallback.CacheRead
	}
	r.CacheRead, _ = parse(deref(read), cacheDef, true)
	if fb {
		cacheDef = fallback.CacheWrite
	} else {
		cacheDef = r.Prompt
	}
	r.CacheWrite, _ = parse(deref(write), cacheDef, true)
	return r, true
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Lookup finds the listing for a model of a provider OpenRouter mirrors.
func (c *Catalog) Lookup(provider, model string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	for _, id := range IDs(provider, model) {
		if e, ok := c.Models[id]; ok {
			return e, true
		}
	}
	return Entry{}, false
}

var dateSuffix = regexp.MustCompile(`-\d{8}$|-\d{4}-\d{2}-\d{2}$`)
var dashedVersion = regexp.MustCompile(`^(.*\D)-(\d+)-(\d+)$`)

// IDs lists OpenRouter's candidate ids for a model: as given, then undated, then, for
// Anthropic, with a dotted version. Anthropic writes claude-haiku-4-5-20251001 where
// OpenRouter writes anthropic/claude-haiku-4.5.
func IDs(provider, model string) []string {
	switch provider {
	case "openrouter":
		return []string{model}
	case "openai", "anthropic":
	default:
		return nil
	}
	undated := dateSuffix.ReplaceAllString(model, "")
	ids := []string{provider + "/" + model, provider + "/" + undated}
	if provider == "anthropic" {
		if m := dashedVersion.FindStringSubmatch(undated); m != nil {
			ids = append(ids, provider+"/"+m[1]+"-"+m[2]+"."+m[3])
		}
	}
	return uniq(ids)
}

func uniq(s []string) []string {
	out := s[:0]
	seen := map[string]bool{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// Short formats a token count: 950, 12.3k, 1.05M.
func Short(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZeros(strconv.FormatFloat(float64(n)/1000, 'f', 1, 64)) + "k"
	default:
		return trimZeros(strconv.FormatFloat(float64(n)/1e6, 'f', 2, 64)) + "M"
	}
}

func trimZeros(s string) string {
	return strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
}
