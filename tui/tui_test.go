package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/tool"
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

func TestPlaceholderOnlyBeforeFirstEntry(t *testing.T) {
	m := testModel(t)
	if m.input.Placeholder == "" {
		t.Fatal("no placeholder at start")
	}
	m.input.SetValue("/help")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.input.Placeholder != "" {
		t.Fatalf("placeholder kept after an entry: %q", m.input.Placeholder)
	}
}

func TestRunStopsWhenTheREPLEnds(t *testing.T) {
	defer func(n int) { eventBuffer = n }(eventBuffer)
	eventBuffer = 0 // the first event blocks, as a full buffer would
	dir := t.TempDir()
	a, err := app.New(app.Options{Mock: "../mock/say-hi.json", Root: dir, StateDir: dir, CacheDir: dir, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(context.Background())
	m := newModel(life, a, Options{})
	m.Update(prepMsg{})
	m.start("hi") // nothing listens, as after the UI exits
	stop()
	done := make(chan struct{})
	go func() { m.tasks.wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent goroutine is still blocked on its event channel")
	}
}

func TestStatusBarFitsTheWidth(t *testing.T) {
	m := testModel(t)
	m.provider, m.modelID, m.effort, m.used = "openrouter", "anthropic/claude-opus-5-20260101", "xhigh", 1200
	m.app.SetPermissions(permission.Ask, m.app.Ask())
	cost := 0.03
	m.session.Cost = &cost
	for _, w := range []int{20, 30, 40, 50, 60, 80, 120} {
		m.width = w
		bar := ansi.Strip(m.statusBar())
		if n := ansi.StringWidth(bar); n > w {
			t.Errorf("width %d: bar is %d columns: %q", w, n, bar)
		}
		// cost and effort go before the mode
		if strings.Contains(bar, "~$") || strings.Contains(bar, "$0.03") {
			if !strings.Contains(bar, " ask ") {
				t.Errorf("width %d: cost kept, mode dropped: %q", w, bar)
			}
		}
		if w == 120 && !(strings.Contains(bar, "xhigh") && strings.Contains(bar, "ask") && strings.Contains(bar, "ctx")) {
			t.Errorf("width %d: segments dropped with room to spare: %q", w, bar)
		}
		if w == 60 && !strings.Contains(bar, " ask ") {
			t.Errorf("width %d: mode dropped: %q", w, bar)
		}
	}
}

func TestPickerKeys(t *testing.T) {
	m := testModel(t)
	items := make([]string, 25)
	for i := range items {
		items[i] = string(rune('a'+i%26)) + "-model"
	}
	m.picker = newPicker("x", items, "", nil)
	p := m.picker
	keys := func(ks ...tea.KeyPressMsg) {
		for _, k := range ks {
			m.Update(k)
		}
	}
	keys(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if p.cursor != pickerRows {
		t.Errorf("pgdown: cursor %d", p.cursor)
	}
	keys(tea.KeyPressMsg{Code: tea.KeyEnd})
	if p.cursor != 24 {
		t.Errorf("end: cursor %d", p.cursor)
	}
	keys(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if p.cursor != 24 {
		t.Errorf("pgdown past the end: cursor %d", p.cursor)
	}
	keys(tea.KeyPressMsg{Code: tea.KeyPgUp}, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if p.cursor != 4 {
		t.Errorf("pgup: cursor %d", p.cursor)
	}
	keys(tea.KeyPressMsg{Code: tea.KeyHome})
	if p.cursor != 0 {
		t.Errorf("home: cursor %d", p.cursor)
	}
	keys(tea.KeyPressMsg{Code: 'x', Text: "x"}, tea.KeyPressMsg{Code: 'é', Text: "é"}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if p.filter != "x" {
		t.Errorf("backspace left %q", p.filter)
	}
	keys(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if p.filter != "" || len(p.matches()) != 25 {
		t.Errorf("ctrl+u left %q", p.filter)
	}
}

func TestAllowedCallsSkipThePreview(t *testing.T) {
	m := testModel(t)
	outside := permission.Reason{Kind: permission.Outside, Text: "edit outside /r"}
	m.always[allowance{"edit", permission.Outside}] = true
	previews := 0
	ask := askVia(make(chan tea.Msg), func(tool.Tool, llm.ToolCall) string { previews++; return "diff" }, m.allowed)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // nothing answers; the preview is made before the question is sent
	ask(ctx, nil, llm.ToolCall{Name: "edit"}, "edit x", outside)
	if previews != 0 {
		t.Error("an allowed call was previewed")
	}
	ask(ctx, nil, llm.ToolCall{Name: "write"}, "write x", outside)
	if previews != 1 {
		t.Error("a call that asks was not previewed")
	}
}

func TestRunWaitsForBackgroundCommands(t *testing.T) {
	m := testModel(t)
	started, release := make(chan struct{}), make(chan struct{})
	go m.background(func() tea.Msg { close(started); <-release; return nil })()
	<-started
	waited := make(chan struct{})
	go func() { m.tasks.wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("wait returned while a command ran")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-waited
	ran := false
	m.background(func() tea.Msg { ran = true; return nil })()
	if ran {
		t.Error("a command started after wait")
	}
}

func TestStatusBarFlagsAFullContext(t *testing.T) {
	m := testModel(t)
	m.width, m.window = 120, 1000
	for used, want := range map[int64]bool{500: false, 849: false, 850: true, 990: true} {
		m.used = used
		if got := strings.Contains(ansi.Strip(m.statusBar()), "%!"); got != want {
			t.Errorf("used %d of 1000: flagged %v", used, got)
		}
	}
}

func TestQueuedPromptsAreShown(t *testing.T) {
	m := testModel(t)
	m.queue = []string{"first\nsecond line", "next \x1b]0;x\x07"}
	v := ansi.Strip(m.View().Content)
	if !strings.Contains(v, "queued: first ...") || !strings.Contains(v, "queued: next ]0;x") {
		t.Errorf("queued prompts not shown safely:\n%s", v)
	}
}

func TestPickerEnterWithNoMatchStaysOpen(t *testing.T) {
	m := testModel(t)
	m.picker = newPicker("x", []string{"a", "b"}, "", func(string) tea.Cmd { t.Fatal("chose"); return nil })
	m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.picker == nil {
		t.Fatal("enter with no match closed the picker")
	}
}

func TestStatusBarShowsTheRoot(t *testing.T) {
	m := testModel(t)
	m.width = 200
	if bar := ansi.Strip(m.statusBar()); !strings.Contains(bar, filepath.Base(m.app.Root())) {
		t.Errorf("bar %q does not show root %s", bar, m.app.Root())
	}
}

func TestHistorySearch(t *testing.T) {
	m := testModel(t)
	m.hist.Entries = []string{"fix the parser", "run tests", "fix the parser", "two\nlines"}
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.picker == nil {
		t.Fatal("ctrl+r opened no picker")
	}
	if got := strings.Join(m.picker.items, ","); got != "two\nlines,fix the parser,run tests" {
		t.Errorf("items %q", got)
	}
	for _, r := range "test" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.picker != nil || m.input.Value() != "run tests" || len(m.queue) != 0 {
		t.Errorf("after enter: input %q, queue %v", m.input.Value(), m.queue)
	}
}

func TestCopyTakesTheLastAnswer(t *testing.T) {
	m := testModel(t)
	if m.command("/copy") != nil {
		t.Error("/copy with no answer set the clipboard")
	}
	m.event(agent.Text{Text: "first"})
	m.event(agent.Response{})
	m.event(agent.Text{Text: "second answer"})
	m.event(agent.Response{})
	m.event(agent.Response{}) // a response with only tool calls keeps the last text
	if m.lastReply != "second answer" || m.command("/copy") == nil {
		t.Errorf("lastReply %q", m.lastReply)
	}
}
