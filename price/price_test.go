package price

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/OpenRouterTeam/go-sdk/models/components"

	"github.com/shakfu/gilda/llm"
)

func str(s string) *string { return &s }

func TestCostPricesCacheAndTiers(t *testing.T) {
	min := float64(1000)
	cat := FromModels([]components.Model{{
		ID: "openai/gpt-x",
		Pricing: components.PublicPricing{
			Prompt: "0.000001", Completion: "0.00001", InputCacheRead: str("0.0000001"),
			Overrides: []components.PricingOverride{{MinPromptTokens: &min, Prompt: str("0.000002")}},
		},
	}})
	e, ok := cat.Lookup("openai", "gpt-x")
	if !ok {
		t.Fatal("not found")
	}
	short, _ := e.Cost(llm.Usage{Input: 1000, CacheRead: 400, Output: 100})
	// 600 fresh at 1e-6, 400 cached at 1e-7, 100 out at 1e-5.
	if want := 600e-6 + 400e-7 + 100e-5; math.Abs(short-want) > 1e-12 {
		t.Fatalf("short prompt cost %g, want %g", short, want)
	}
	long, _ := e.Cost(llm.Usage{Input: 2000, Output: 0})
	// The tier changes the prompt rate and inherits the rest.
	if want := 2000 * 2e-6; math.Abs(long-want) > 1e-12 {
		t.Fatalf("long prompt cost %g, want %g", long, want)
	}
	if e.Tiers[0].Rates.CacheRead != 1e-7 {
		t.Fatalf("tier lost the base cache rate: %+v", e.Tiers[0])
	}
}

func TestRoutePricedModelsHaveNoRate(t *testing.T) {
	cat := FromModels([]components.Model{{ID: "openrouter/auto", Pricing: components.PublicPricing{Prompt: "-1", Completion: "-1"}}})
	if _, ok := cat.Models["openrouter/auto"].Cost(llm.Usage{Input: 1}); ok {
		t.Fatal("priced a per-route model")
	}
}

func TestMissingCacheRatesFallBackToThePromptRate(t *testing.T) {
	cat := FromModels([]components.Model{{ID: "a/b", Pricing: components.PublicPricing{Prompt: "0.000003", Completion: "0.00001"}}})
	r := cat.Models["a/b"].Rates
	if r.CacheRead != 3e-6 || r.CacheWrite != 3e-6 {
		t.Fatalf("rates %+v", r)
	}
}

func TestIDsMapVendorNamesToOpenRouters(t *testing.T) {
	cases := []struct {
		provider, model string
		want            string
	}{
		{"anthropic", "claude-haiku-4-5-20251001", "anthropic/claude-haiku-4.5"},
		{"anthropic", "claude-opus-5", "anthropic/claude-opus-5"},
		{"openai", "gpt-5.5-2026-01-01", "openai/gpt-5.5"},
		{"openrouter", "x/y", "x/y"},
	}
	for _, c := range cases {
		if ids := IDs(c.provider, c.model); !slices.Contains(ids, c.want) {
			t.Errorf("IDs(%s, %s) = %v, want %s among them", c.provider, c.model, ids, c.want)
		}
	}
	if IDs("llamacpp", "x") != nil {
		t.Error("a local provider was mapped")
	}
}

func TestShort(t *testing.T) {
	for n, want := range map[int64]string{950: "950", 12300: "12.3k", 200000: "200k", 1050000: "1.05M", 1000000: "1M"} {
		if got := Short(n); got != want {
			t.Errorf("Short(%d) = %q, want %q", n, got, want)
		}
	}
}

// The highest threshold a prompt passes decides the rate, in whatever order the tiers come.
func TestTierOrderDoesNotMatter(t *testing.T) {
	base := Rates{Prompt: 1}
	low, high := Tier{MinPrompt: 100, Rates: Rates{Prompt: 2}}, Tier{MinPrompt: 1000, Rates: Rates{Prompt: 3}}
	for _, tiers := range [][]Tier{{low, high}, {high, low}} {
		e := Entry{Rates: &base, Tiers: tiers}
		for input, want := range map[int64]float64{50: 50, 500: 1000, 5000: 15000} {
			if got, _ := e.Cost(llm.Usage{Input: input}); got != want {
				t.Errorf("tiers %v, input %d: cost %g, want %g", tiers, input, got, want)
			}
		}
	}
}

func TestImagesFollowTheListedModalities(t *testing.T) {
	cat := FromModels([]components.Model{
		{ID: "a/vision", Architecture: components.ModelArchitecture{InputModalities: []components.InputModality{"text", "image"}}},
		{ID: "a/text", Architecture: components.ModelArchitecture{InputModalities: []components.InputModality{"text"}}},
	})
	if ok, known := cat.Models["a/vision"].Images(); !ok || !known {
		t.Fatal("vision model")
	}
	if ok, known := cat.Models["a/text"].Images(); ok || !known {
		t.Fatal("text model")
	}
	// A list cached before modalities were recorded says nothing.
	var old Catalog
	if err := json.Unmarshal([]byte(`{"models":{"a/b":{"context":1}}}`), &old); err != nil {
		t.Fatal(err)
	}
	if _, known := old.Models["a/b"].Images(); known {
		t.Fatal("an old entry claimed to know")
	}
}
