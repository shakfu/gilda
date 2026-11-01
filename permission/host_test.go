package permission

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

func TestHostAllowed(t *testing.T) {
	entries := []string{"pkg.go.dev", "*.githubusercontent.com", "127.0.0.1"}
	for host, want := range map[string]bool{
		"pkg.go.dev":                     true,
		"PKG.GO.DEV.":                    true,
		"pkg.go.dev:443":                 true,
		"evil-pkg.go.dev":                false,
		"go.dev":                         false,
		"raw.githubusercontent.com":      true,
		"a.b.githubusercontent.com":      true,
		"githubusercontent.com":          false,
		"githubusercontent.com.evil.com": false,
		"127.0.0.1:8080":                 true,
		"":                               false,
	} {
		if got := hostAllowed(entries, host); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	for _, e := range []string{"pkg.go.dev", "*.example.com", "localhost", "10.0.0.1", "::1"} {
		if err := checkHost(e); err != nil {
			t.Errorf("rejected %q: %v", e, err)
		}
	}
	for _, e := range []string{"*", "https://pkg.go.dev", "pkg.go.dev/x", "a.*.com", "", "-bad.com"} {
		if err := checkHost(e); err == nil {
			t.Errorf("accepted %q", e)
		}
	}
}

// liar claims to be read-only but contacts hosts; the network declaration wins.
type liar struct{ custom }

func (liar) ReadOnly() bool                          { return true }
func (liar) Hosts(json.RawMessage) ([]string, error) { return []string{"evil.com"}, nil }
func (liar) Paths(json.RawMessage) ([]string, error) { return nil, nil }

func TestNetworkDecisions(t *testing.T) {
	root := t.TempDir()
	noop := func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil }
	arg := func(key string) func(json.RawMessage) ([]string, error) {
		return func(a json.RawMessage) ([]string, error) {
			var v map[string]string
			if err := json.Unmarshal(a, &v); err != nil {
				return nil, err
			}
			if v[key] == "" {
				return nil, nil
			}
			return []string{v[key]}, nil
		}
	}
	fetch, _ := tool.New(tool.Def{Name: "fetch", Hosts: arg("host"), Paths: arg("path"), Run: noop})
	ping, _ := tool.New(tool.Def{Name: "ping", Hosts: arg("host"), Run: noop})
	l := Layers{{Hosts: []string{"pkg.go.dev"}}}
	type c struct {
		t          tool.Tool
		host, path string
	}
	cases := map[string]c{
		"listed":        {fetch, "pkg.go.dev", ""},
		"listed write":  {fetch, "pkg.go.dev", "docs/x.html"},
		"listed out":    {fetch, "pkg.go.dev", "/tmp/x"},
		"listed .git":   {fetch, "pkg.go.dev", ".git/x"},
		"unlisted":      {fetch, "evil.com", ""},
		"no paths decl": {ping, "pkg.go.dev", ""},
		"liar":          {liar{custom{"liar"}}, "", ""},
	}
	want := map[Mode]map[string]verdict{
		Auto:     {"listed": run, "listed write": run, "listed out": askUser, "listed .git": askUser, "unlisted": askUser, "no paths decl": askUser, "liar": askUser},
		Ask:      {"listed": run, "listed write": askUser, "listed out": askUser, "listed .git": askUser, "unlisted": askUser, "no paths decl": askUser, "liar": askUser},
		ReadOnly: {"listed": run, "listed write": refuse, "listed out": refuse, "listed .git": refuse, "unlisted": refuse, "no paths decl": refuse, "liar": refuse},
		All:      {"listed": run, "listed write": run, "listed out": run, "listed .git": run, "unlisted": run, "no paths decl": run, "liar": run},
	}
	for mode, row := range want {
		for name, w := range row {
			cs := cases[name]
			args, _ := json.Marshal(map[string]string{"host": cs.host, "path": cs.path})
			v, why := l.decide(mode, root, cs.t, llm.ToolCall{Name: cs.t.Spec().Name, Arguments: string(args)})
			if v != w {
				t.Errorf("%s / %s: got %d (%s), want %d", mode, name, v, why, w)
			}
		}
	}
	if err := (Rules{Hosts: []string{"https://x.com"}}).Validate(); err == nil {
		t.Error("accepted a URL as a host")
	}
}

// Every unlisted host is named, so an approval covers what the call reaches.
func TestAReasonNamesEveryUnlistedHost(t *testing.T) {
	fetch, err := tool.New(tool.Def{
		Name:  "fetch",
		Hosts: func(json.RawMessage) ([]string, error) { return []string{"A.com:443", "ok.com", "b.com", "a.com"}, nil },
		Run:   func(context.Context, json.RawMessage) (tool.Result, error) { return tool.Result{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	v, why := Layers{{Hosts: []string{"ok.com"}}}.decide(Auto, t.TempDir(), fetch, llm.ToolCall{Name: "fetch", Arguments: "{}"})
	if v != askUser || !slices.Equal(why.Hosts, []string{"a.com", "b.com"}) || !strings.Contains(why.Text, "a.com, b.com, which are") {
		t.Fatalf("verdict %d, reason %+v", v, why)
	}
}
