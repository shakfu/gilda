package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
)

// drive runs cmd to completion the way Bubble Tea would, feeding each message back into the
// model, and returns what was printed, without styling. It reports whether a Quit came out.
// Batches and sequences run in order; spinner ticks are dropped, since each one asks for the
// next.
func drive(m *model, cmd tea.Cmd) (printed string, quit bool) {
	var b strings.Builder
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		switch msg.(type) {
		case nil, spinner.TickMsg:
			return
		case tea.QuitMsg:
			quit = true
			return
		}
		v := reflect.ValueOf(msg)
		if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
			for i := range v.Len() {
				run(v.Index(i).Interface().(tea.Cmd))
			}
			return
		}
		// tea.Println's message type is unexported; its text is read by field name.
		if v.Kind() == reflect.Struct && v.Type().Name() == "printLineMessage" {
			b.WriteString(ansi.Strip(v.FieldByName("messageBody").String()) + "\n")
			return
		}
		_, next := m.Update(msg)
		run(next)
	}
	run(cmd)
	return b.String(), quit
}

// submit types text into the input and presses Enter.
func submit(m *model, text string) (string, bool) {
	m.input.SetValue(text)
	return drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} })
}

func TestCommands(t *testing.T) {
	cases := []struct {
		in    string
		want  []string // substrings of the output
		check func(*model) error
	}{
		{"/help", []string{"/model", "/copy", "/exit", "Ctrl-R searches history", "secret"}, nil},
		{"/thinking", []string{"reasoning display on"}, func(m *model) error {
			return must(m.showThink, "thinking not on")
		}},
		{"/effort high", []string{"effort high"}, func(m *model) error {
			return must(m.effort == "high" && m.app.State.Effort == "high", "effort %q", m.effort)
		}},
		{"/effort huge", []string{"error: effort must be"}, func(m *model) error {
			return must(m.effort == "", "effort %q", m.effort)
		}},
		{"/permissions ask", []string{"permissions ask"}, func(m *model) error {
			return must(m.app.Mode() == permission.Ask, "mode %s", m.app.Mode())
		}},
		{"/permissions some", []string{"error: permissions must be"}, nil},
		{"/models", []string{"* mock  200k", "1 models"}, nil},
		{"/models zzz", []string{"0 models"}, nil},
		{"/model mock", []string{"using mock/mock (200k context)"}, func(m *model) error {
			return must(m.busy == "" && m.window == 200_000, "busy %q window %d", m.busy, m.window)
		}},
		{"/model other", []string{"error:"}, func(m *model) error {
			return must(m.modelID == "mock", "model %q", m.modelID)
		}},
		{"/provider openai", []string{"cannot switch provider in a mock session"}, nil},
		{"/cost", []string{"session: 0 prompts"}, nil},
		{"/nope", []string{"unknown command /nope"}, nil},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			m := testModel(t)
			out, _ := submit(m, c.in)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if c.check != nil {
				if err := c.check(m); err != nil {
					t.Error(err)
				}
			}
			if m.input.Value() != "" || len(m.queue) != 0 {
				t.Errorf("input %q queue %v", m.input.Value(), m.queue)
			}
		})
	}
}

func must(ok bool, format string, args ...any) error {
	if ok {
		return nil
	}
	return fmt.Errorf(format, args...)
}

func TestCommandsWithoutArgumentsOpenPickers(t *testing.T) {
	for _, c := range []struct {
		in    string
		items []string
		pick  string
		check func(*model) bool
	}{
		{"/effort", efforts, "max", func(m *model) bool { return m.effort == "max" }},
		{"/permissions", []string{"auto", "ask", "all", "read-only"}, "read-only", func(m *model) bool { return m.app.Mode() == permission.ReadOnly }},
		{"/model", []string{"mock"}, "mock", func(m *model) bool { return m.modelID == "mock" && m.busy == "" }},
	} {
		t.Run(c.in, func(t *testing.T) {
			m := testModel(t)
			submit(m, c.in)
			if m.picker == nil || !slices.Equal(m.picker.items, c.items) {
				t.Fatalf("picker %+v", m.picker)
			}
			for _, r := range c.pick {
				m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} })
			if m.picker != nil || !c.check(m) {
				t.Errorf("picking %q did not apply", c.pick)
			}
		})
	}
}

func TestCommandsThatChangeTheAgentWaitForTheTurn(t *testing.T) {
	for _, in := range []string{"/model mock", "/models", "/provider", "/effort high", "/clear", "/permissions ask", "/resume x"} {
		m := testModel(t)
		m.running = true
		if out, _ := submit(m, in); !strings.Contains(out, "waits for the current turn") {
			t.Errorf("%s ran during a turn: %q", in, out)
		}
	}
	m := testModel(t)
	m.running = true
	if out, _ := submit(m, "/cost"); !strings.Contains(out, "session:") {
		t.Errorf("/cost waited: %q", out)
	}
}

func TestExitQuits(t *testing.T) {
	for _, in := range []string{"/exit", "/quit"} {
		if _, quit := submit(testModel(t), in); !quit {
			t.Errorf("%s did not quit", in)
		}
	}
	m := testModel(t)
	if _, quit := drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl} }); !quit {
		t.Error("ctrl+d on an empty line did not quit")
	}
	m.input.SetValue("draft")
	if _, quit := drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }); quit || m.input.Value() != "" {
		t.Error("ctrl+c on a draft quit or kept it")
	}
}

func TestTabCompletion(t *testing.T) {
	m := testModel(t)
	tab := func() { drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyTab} }) }
	m.input.SetValue("/he")
	tab()
	if m.input.Value() != "/help" {
		t.Errorf("/he completed to %q", m.input.Value())
	}
	m.input.SetValue("/c")
	var got []string
	for range 4 {
		tab()
		got = append(got, m.input.Value())
	}
	if want := []string{"/clear", "/cost", "/copy", "/clear"}; !slices.Equal(got, want) {
		t.Errorf("/c cycled %v, want %v", got, want)
	}
	m.input.SetValue("hello")
	tab()
	if m.input.Value() != "hello" {
		t.Errorf("plain text completed to %q", m.input.Value())
	}
	m.input.SetValue("/model mo")
	tab()
	if m.picker == nil || m.picker.filter != "mo" || len(m.picker.matches()) != 1 {
		t.Errorf("/model tab: picker %+v", m.picker)
	}
}

func TestHistoryBrowsing(t *testing.T) {
	m := testModel(t)
	m.hist.Entries = []string{"one", "two"}
	m.histPos = 2
	m.input.SetValue("draft")
	key := func(c rune) { m.Update(tea.KeyPressMsg{Code: c}) }
	key(tea.KeyUp)
	key(tea.KeyUp)
	key(tea.KeyUp) // past the oldest entry
	if m.input.Value() != "one" {
		t.Errorf("up up up: %q", m.input.Value())
	}
	key(tea.KeyDown)
	key(tea.KeyDown)
	if m.input.Value() != "draft" {
		t.Errorf("down to the draft: %q", m.input.Value())
	}
	m.input.SetValue("a\nb")
	key(tea.KeyUp)
	if m.input.Value() != "a\nb" {
		t.Error("up in a multi-line input browsed history")
	}
}

func TestFinishReports(t *testing.T) {
	for _, c := range []struct {
		d       doneMsg
		stopped bool
		want    string
		queue   bool // whether the queue survives
	}{
		{doneMsg{err: context.Canceled}, true, "cancelled", false},
		{doneMsg{err: agent.ErrContextFull}, false, "no longer fits the context window", false},
		{doneMsg{err: errors.New("boom")}, false, "error: boom", true},
		{doneMsg{res: agent.Result{Stop: llm.StopMaxTokens, Turns: 1}}, false, "cut off at the output limit", true},
		{doneMsg{res: agent.Result{Turns: 1}}, false, "in ", true},
	} {
		m := testModel(t)
		m.running, m.stopped, m.cancel, m.queue = true, c.stopped, func() {}, []string{"later"}
		m.finish(c.d)
		out := ansi.Strip(strings.Join(m.lines, "\n"))
		if !strings.Contains(out, c.want) || m.running {
			t.Errorf("%v: %q", c.d.err, out)
		}
		if (len(m.queue) > 0) != c.queue {
			t.Errorf("%v: queue %v", c.d.err, m.queue)
		}
	}
}

// TestMockSession runs two prompts through the mock provider. The second runs a bash call,
// which auto mode allows.
func TestMockSession(t *testing.T) {
	m := testModel(t)
	m.queue = []string{"hi", "run something"}
	out, _ := drive(m, m.next())
	for _, w := range []string{"> hi", "Hi. I am gilda", `fmt.Println("hello")`, "> run something",
		"[tool] $ echo hello from bash", "Done. That was the last scripted reply."} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if m.running || len(m.queue) != 0 || m.prompts != 2 {
		t.Errorf("running %v queue %v prompts %d", m.running, m.queue, m.prompts)
	}
	if m.lastReply != "Done. That was the last scripted reply." {
		t.Errorf("lastReply %q", m.lastReply)
	}
	if m.session.Input != 3000 {
		t.Errorf("session input %d", m.session.Input)
	}
}

func TestPickerViewScrollsAndCuts(t *testing.T) {
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf("item-%02d", i)
	}
	items[29] = "item-29 " + strings.Repeat("x", 100)
	p := newPicker("x", items, "item-03", nil)
	lines := strings.Split(strings.TrimSuffix(ansi.Strip(p.view(NewStyles(), 40)), "\n"), "\n")
	if len(lines) != pickerRows+1 || lines[4] != "* item-03" || !strings.Contains(lines[0], "(30/30") {
		t.Errorf("first page:\n%s", strings.Join(lines, "\n"))
	}
	p.cursor = 29
	lines = strings.Split(strings.TrimSuffix(ansi.Strip(p.view(NewStyles(), 40)), "\n"), "\n")
	last := lines[len(lines)-1]
	if lines[1] != "  item-20" || ansi.StringWidth(last) > 39 || !strings.HasSuffix(last, "...") {
		t.Errorf("last page:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSessionsAndResume(t *testing.T) {
	m := testModel(t)
	if out, _ := submit(m, "/sessions"); !strings.Contains(out, "no saved sessions") {
		t.Fatalf("empty: %q", out)
	}
	if _, err := m.app.Agent.Run(context.Background(), "first prompt", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.app.Save(); err != nil {
		t.Fatal(err)
	}
	id := m.app.Agent.SessionID
	if out, _ := submit(m, "/sessions"); !strings.Contains(out, id) || !strings.Contains(out, "first prompt") {
		t.Fatalf("list: %q", out)
	}
	submit(m, "/clear")
	if len(m.app.Agent.History) != 0 {
		t.Fatal("clear kept the history")
	}
	out, _ := submit(m, "/resume "+id[len("gilda-"):][:5])
	if len(m.app.Agent.History) != 2 || m.app.Agent.SessionID != id || !strings.Contains(out, "resumed "+id) {
		t.Fatalf("resume: %q, %d messages", out, len(m.app.Agent.History))
	}
	if out, _ := submit(m, "/resume nope"); !strings.Contains(out, "no saved session") {
		t.Fatalf("unknown id: %q", out)
	}
	submit(m, "/clear")
	submit(m, "/resume")
	if m.picker == nil || len(m.picker.items) != 1 {
		t.Fatalf("picker %+v", m.picker)
	}
	drive(m, func() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} })
	if m.app.Agent.SessionID != id {
		t.Fatal("picking did not resume")
	}
}
