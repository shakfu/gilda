package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/price"
	"github.com/shakfu/gilda/provider"
)

type command struct {
	name, args, help string
}

var commands = []command{
	{"/model", "[id]", "switch model; without an id, pick from the provider's list"},
	{"/models", "[filter]", "list the provider's models"},
	{"/provider", "[id]", "switch provider; without an id, pick one"},
	{"/effort", "[level]", "reasoning effort: low, medium, high, xhigh, max, or default"},
	{"/permissions", "[mode]", "auto, ask, all or read-only; see /help"},
	{"/thinking", "", "show or hide reasoning as it streams"},
	{"/clear", "", "start a new conversation; session cost is kept"},
	{"/cost", "", "session tokens and cost"},
	{"/help", "", "list commands and keys"},
	{"/exit", "", "leave; so do /quit, Ctrl-D, and Ctrl-C on an empty line"},
}

var efforts = []string{"default", "low", "medium", "high", "xhigh", "max"}

func shortTokens(n int64) string { return price.Short(n) }

// command runs a slash command. Commands that change the agent wait for a run to finish, since
// the run reads the agent from another goroutine.
func (m *model) command(text string) tea.Cmd {
	fields := strings.Fields(text)
	name, arg := fields[0], strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	// /models is async too; one background step at a time keeps busy owned by a single step.
	mutates := name == "/model" || name == "/models" || name == "/provider" || name == "/effort" ||
		name == "/clear" || name == "/permissions"
	if mutates && (m.running || m.busy != "") {
		m.out(m.st.Warn.Render(name + " waits for the current turn; press Esc to cancel it"))
		return nil
	}
	switch name {
	case "/exit", "/quit":
		return tea.Quit
	case "/help":
		for _, c := range commands {
			m.out("  " + m.st.Accent.Render(fmt.Sprintf("%-13s", c.name)) + m.st.Dim.Render(fmt.Sprintf("%-9s", c.args)) + c.help)
		}
		m.out(m.st.Dim.Render("  Enter sends | Shift-Enter, Alt-Enter or Ctrl-J adds a line | Up/Down history"))
		m.out(m.st.Dim.Render("  Tab completes | Esc cancels a turn | typing during a turn queues the next prompt"))
		m.out(m.st.Dim.Render("  model ids take a provider prefix to switch both: /model openrouter:openai/gpt-5.5"))
		m.out(m.st.Dim.Render("  permissions: auto asks before write/edit outside the working directory; ask asks"))
		m.out(m.st.Dim.Render("  before every call but read; all never asks; read-only refuses all but read"))
	case "/clear":
		m.app.Agent.Reset()
		m.used = 0
		clear(m.always)
		m.out(m.st.Dim.Render("new conversation"))
	case "/cost":
		m.out(m.st.Dim.Render(m.sessionLine()))
	case "/permissions":
		set := func(s string) tea.Cmd {
			mode, err := permission.Parse(s)
			if err != nil {
				m.out(m.st.Error.Render("error: " + err.Error()))
				return nil
			}
			m.app.SetPermissions(mode, m.app.Ask())
			clear(m.always)
			m.out(m.st.Dim.Render("permissions " + string(mode)))
			return nil
		}
		if arg == "" {
			var modes []string
			for _, md := range permission.Modes {
				modes = append(modes, string(md))
			}
			m.picker = newPicker("permissions", modes, string(m.app.Mode()), set)
			return nil
		}
		return set(arg)
	case "/thinking":
		m.showThink = !m.showThink
		m.out(m.st.Dim.Render(fmt.Sprintf("reasoning display %s", onOff(m.showThink))))
	case "/effort":
		if arg == "" {
			m.picker = newPicker("effort", efforts, orDefault(m.effort), func(s string) tea.Cmd {
				return m.setEffort(s)
			})
			return nil
		}
		return m.setEffort(arg)
	case "/model":
		if arg == "" {
			return m.fetchModels(true, "")
		}
		return m.switchTo("", arg)
	case "/models":
		return m.fetchModels(false, arg)
	case "/provider":
		if arg == "" {
			var ids []string
			for _, e := range provider.Registry {
				if m.app.HasKey(e.ID) {
					ids = append(ids, e.ID)
				}
			}
			m.picker = newPicker("provider", ids, m.provider, func(s string) tea.Cmd { return m.switchTo(s, "") })
			return nil
		}
		return m.switchTo(arg, "")
	default:
		m.out(m.st.Error.Render("unknown command " + name + "; /help lists them"))
	}
	return nil
}

func (m *model) setEffort(level string) tea.Cmd {
	if level == "default" {
		level = ""
	}
	if err := m.app.SetEffort(level); err != nil {
		m.out(m.st.Error.Render("error: " + err.Error()))
		return nil
	}
	m.sync()
	m.out(m.st.Dim.Render("effort " + orDefault(level)))
	return nil
}

func (m *model) switchTo(providerID, modelID string) tea.Cmd {
	m.busy = "switching"
	a, ctx := m.app, m.ctx
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		return switchedMsg{err: a.Switch(ctx, providerID, modelID)}
	})
}

func (m *model) fetchModels(pick bool, filter string) tea.Cmd {
	m.busy = "listing models"
	a, ctx := m.app, m.ctx
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		models, err := a.Models(ctx)
		return modelsMsg{models: models, err: err, pick: pick, filter: filter}
	})
}

func (m *model) showModels(msg modelsMsg) tea.Cmd {
	if msg.err != nil {
		m.out(m.st.Error.Render("error: " + msg.err.Error()))
		return nil
	}
	ids := make([]string, len(msg.models))
	for i, x := range msg.models {
		ids[i] = x.ID
	}
	if msg.pick {
		m.picker = newPicker(m.provider+" model", ids, m.modelID, func(s string) tea.Cmd { return m.switchTo("", s) })
		m.picker.filter = msg.filter
		return nil
	}
	f := strings.ToLower(msg.filter)
	n := 0
	for _, x := range msg.models {
		if f != "" && !strings.Contains(strings.ToLower(x.ID), f) {
			continue
		}
		n++
		mark, style := "  ", m.st.Match
		if x.ID == m.modelID {
			mark, style = "* ", m.st.Model
		}
		line := mark + style.Render(x.ID)
		if x.Context > 0 {
			line += m.st.Dim.Render("  " + shortTokens(x.Context))
		}
		m.out(line)
	}
	m.out(m.st.Dim.Render(fmt.Sprintf("%d models", n)))
	return nil
}

// complete handles Tab: command names, then model ids after "/model ".
func (m *model) complete() tea.Cmd {
	v := m.input.Value()
	if rest, ok := strings.CutPrefix(v, "/model "); ok && !m.running && m.busy == "" {
		return m.fetchModels(true, strings.TrimSpace(rest))
	}
	if !strings.HasPrefix(v, "/") || strings.Contains(v, " ") {
		return nil
	}
	if m.tabSeed == "" {
		m.tabSeed, m.tabIdx = v, 0
	}
	var matches []string
	for _, c := range commands {
		if strings.HasPrefix(c.name, m.tabSeed) {
			matches = append(matches, c.name)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	m.input.SetValue(matches[m.tabIdx%len(matches)])
	m.tabIdx++
	return nil
}

// picker chooses one item from a list, filtered as the user types.
type picker struct {
	title, current, filter string
	items                  []string
	cursor                 int
	choose                 func(string) tea.Cmd
}

const pickerRows = 10

func newPicker(title string, items []string, current string, choose func(string) tea.Cmd) *picker {
	p := &picker{title: title, items: items, current: current, choose: choose}
	for i, it := range p.matches() {
		if it == current {
			p.cursor = i
		}
	}
	return p
}

// matches keeps items containing every word of the filter, ignoring case.
func (p *picker) matches() []string {
	words := strings.Fields(strings.ToLower(p.filter))
	var out []string
outer:
	for _, it := range p.items {
		low := strings.ToLower(it)
		for _, w := range words {
			if !strings.Contains(low, w) {
				continue outer
			}
		}
		out = append(out, it)
	}
	return out
}

func (m *model) pickerKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	matches := p.matches()
	switch msg.String() {
	case "esc", "ctrl+c":
		m.picker = nil
	case "enter":
		m.picker = nil
		if len(matches) > 0 {
			return tea.Sequence(p.choose(matches[min(p.cursor, len(matches)-1)]), m.flush())
		}
	case "up", "ctrl+p":
		p.cursor = max(p.cursor-1, 0)
	case "down", "ctrl+n", "tab":
		p.cursor = min(p.cursor+1, max(len(matches)-1, 0))
	case "backspace":
		if p.filter != "" {
			p.filter = p.filter[:len(p.filter)-1]
			p.cursor = 0
		}
	default:
		if msg.Text != "" {
			p.filter += msg.Text
			p.cursor = 0
		}
	}
	return nil
}

func (p *picker) view(st Styles, width int) string {
	matches := p.matches()
	var b strings.Builder
	b.WriteString(st.Accent.Bold(true).Render(p.title) + st.Dim.Render(" filter: ") + p.filter +
		st.Dim.Render(fmt.Sprintf("  (%d/%d, Enter picks, Esc closes)", len(matches), len(p.items))) + "\n")
	start := 0
	if p.cursor >= pickerRows {
		start = p.cursor - pickerRows + 1
	}
	for i := start; i < len(matches) && i < start+pickerRows; i++ {
		it := matches[i]
		mark := "  "
		if it == p.current {
			mark = "* "
		}
		line := ansi.Truncate(mark+it, width-1, "...")
		if i == p.cursor {
			line = st.Selected.Render(line)
		} else {
			line = st.Match.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orDefault(s string) string {
	if s == "" {
		return "default"
	}
	return s
}
