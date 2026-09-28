package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
)

func testModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(app.Options{Mock: "../mock/say-hi.json", Root: dir, StateDir: dir, CacheDir: dir, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), a, Options{})
	m.Update(prepMsg{})
	return m
}

// ask sends an approval question and reports whether the model answered it without showing it.
func ask(m *model, name string, why permission.Reason) (answered, ok bool) {
	reply := make(chan bool, 1)
	m.Update(approvalMsg{call: llm.ToolCall{Name: name}, label: name + " x", why: why, reply: reply})
	select {
	case ok = <-reply:
		return true, ok
	default:
		return false, false
	}
}

func press(m *model, k string) {
	m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
}

func TestAlwaysCoversOneToolAndKind(t *testing.T) {
	m := testModel(t)
	outside := permission.Reason{Kind: permission.Outside, Text: "edit outside /r"}
	if answered, _ := ask(m, "edit", outside); answered || m.approval == nil {
		t.Fatal("the first call was not shown")
	}
	reply := m.approval.reply
	press(m, "a")
	if ok := <-reply; !ok || m.approval != nil {
		t.Fatal("a did not approve the call")
	}
	if answered, ok := ask(m, "edit", outside); !answered || !ok {
		t.Fatal("the same tool and kind asked again")
	}
	for _, c := range []struct {
		name string
		why  permission.Reason
	}{
		{"write", outside},
		{"edit", permission.Reason{Kind: permission.General, Text: "edit"}},
		{"edit", permission.Reason{Kind: permission.Secret, Text: "edit of .env"}},
		{"edit", permission.Reason{Kind: permission.Protected, Text: "edit under .git"}},
	} {
		m.approval = nil
		if answered, _ := ask(m, c.name, c.why); answered {
			t.Errorf("%s (%s) was approved by an earlier a", c.name, c.why)
		}
	}
	m.command("/clear")
	m.approval = nil
	if answered, _ := ask(m, "edit", outside); answered {
		t.Error("/clear kept the allowance")
	}
}

func TestSecretsCannotBeAlwaysAllowed(t *testing.T) {
	m := testModel(t)
	secret := permission.Reason{Kind: permission.Secret, Text: "read of .env"}
	ask(m, "read", secret)
	if strings.Contains(m.View().Content, "a always") {
		t.Error("the prompt offers a for a secret")
	}
	reply := m.approval.reply
	press(m, "a")
	select {
	case <-reply:
		t.Fatal("a answered a secret's approval")
	default:
	}
	press(m, "y")
	if ok := <-reply; !ok {
		t.Fatal("y did not approve")
	}
	if answered, _ := ask(m, "read", secret); answered {
		t.Error("a secret was approved without asking")
	}
}

func TestModelTextIsEscaped(t *testing.T) {
	cases := map[string]string{
		"title \x1b]0;pwned\x07":   `title \x1b]0;pwned\x07`,
		"clip \x1b]52;c;aGk=\x07":  `clip \x1b]52;c;aGk=\x07`,
		"line\r":                   "line",
		"rtl \u202eevil":           `rtl \u202eevil`,
		"persian \u200c joiner ok": "persian \u200c joiner ok",
	}
	for in, want := range cases {
		if got := text(in); got != want {
			t.Errorf("text(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"a\u200bb":   `a\u200bb`,
		"a\u2066b":   `a\u2066b`,
		"\ufeffx":    `\ufeffx`,
		"tab\tok\n":  "tab\tok\n",
		"bell\x07":   `bell\x07`,
		"c1\u009bx":  `c1\u009bx`,
		"plain text": "plain text",
	} {
		if got := Visible(in); got != want {
			t.Errorf("Visible(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecliningTrustSwitchesToAsk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(app.Options{Mock: "../mock/say-hi.json", Root: dir, StateDir: dir, CacheDir: dir, ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), a, Options{})
	m.Update(prepMsg{})
	if m.trust != dir || m.busy == "" {
		t.Fatalf("no trust question: %q", m.trust)
	}
	m.queue = []string{"hi"}
	if m.next() != nil {
		t.Fatal("a prompt started before the answer")
	}
	press(m, "n")
	if m.trust != "" || a.Mode() != permission.Ask {
		t.Fatalf("after n: trust %q mode %s", m.trust, a.Mode())
	}
}
