// Package tui is gilda's REPL. It runs inline, not on the alternate screen: finished output goes
// into the terminal's own scrollback, and only the streaming line, the input and the status bar
// are redrawn.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/prompt"
	"github.com/shakfu/gilda/state"
	"github.com/shakfu/gilda/tool"
)

type Options struct {
	Version string
	Color   bool
}

// Run starts the REPL and returns when the user leaves it.
func Run(ctx context.Context, a *app.App, opts Options) error {
	// life ends with the REPL, so a run still sending events stops instead of blocking forever.
	life, stop := context.WithCancel(ctx)
	m := newModel(life, a, opts)
	popts := []tea.ProgramOption{tea.WithContext(life)}
	if !opts.Color {
		popts = append(popts, tea.WithColorProfile(colorprofile.Ascii))
	}
	_, err := tea.NewProgram(m, popts...).Run()
	stop()
	m.tasks.wait()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		err = nil
	}
	if m.prompts > 0 {
		fmt.Fprintln(Writer(os.Stdout, opts.Color), m.st.Dim.Render(m.sessionLine()))
	}
	return err
}

type (
	eventMsg struct{ ev agent.Event }
	doneMsg  struct {
		res agent.Result
		err error
	}
	prepMsg struct {
		warns []error
		err   error
	}
	// approvalMsg asks the user whether a call may run; the agent waits on reply.
	approvalMsg struct {
		call    llm.ToolCall
		label   string
		why     permission.Reason
		preview string // such as a diff; empty when there is none
		reply   chan bool
	}
	switchedMsg struct{ err error }
	modelsMsg   struct {
		models []llm.Model
		err    error
		// pick opens the picker; otherwise the list is printed, filtered.
		pick   bool
		filter string
	}
)

type model struct {
	ctx  context.Context
	app  *app.App
	opts Options
	st   Styles
	md   markdown

	width int
	input textarea.Model
	spin  spinner.Model
	hist  *state.History
	// histPos indexes hist.Entries while browsing; len(Entries) means the draft.
	histPos int
	draft   string

	// busy names a background step that blocks new prompts, such as "loading".
	busy    string
	running bool
	// stopped is set when the user cancels the run, whatever error the provider returns.
	stopped bool
	cancel  context.CancelFunc
	events  chan tea.Msg
	tasks   tasks // goroutines that Run waits for
	started time.Time
	queue   []string

	partial   string // assistant text after the last newline
	reply     string // assistant text of the current response
	lastReply string // the last response with text, for /copy
	thinking  string // reasoning after the last newline
	showThink bool
	phase     string // what the status bar says during a run
	lines     []string

	// Display copies, refreshed when no goroutine touches the agent.
	provider, modelID, effort string
	window, used              int64
	session                   llm.Usage
	prompts                   int

	picker *picker
	// approval is the question waiting for y, n or a.
	approval *approvalMsg
	// trust is the directory waiting for y or n; see app.App.Trust.
	trust string
	// always holds what the user allowed until /clear or /permissions: a tool, for one kind of
	// reason. A secret or protected path is never added.
	always map[allowance]bool
	// Tab cycles through the commands that start with tabSeed; tabLast is the one it set last.
	tabIdx           int
	tabSeed, tabLast string
}

func newModel(ctx context.Context, a *app.App, opts Options) *model {
	st := NewStyles()
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Prompt = "> "
	in.Placeholder = "Ask gilda. Enter sends, Shift-Enter or Ctrl-J adds a line, /help lists commands."
	in.CharLimit = 0
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = 8
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	s := in.Styles()
	s.Focused.CursorLine = lipgloss.NewStyle()
	s.Focused.Prompt = st.Accent.Bold(true)
	s.Focused.Placeholder = st.Dim
	in.SetStyles(s)
	in.Focus()

	hist := state.LoadHistory(a.State.Dir())
	m := &model{
		ctx: ctx, app: a, opts: opts, st: st, md: markdown{st: st, width: 80},
		width: 80, input: in, hist: hist, histPos: len(hist.Entries),
		spin: spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(st.Warn)),
		busy: "loading", always: map[allowance]bool{},
	}
	m.sync()
	return m
}

func (m *model) Init() tea.Cmd {
	a := m.app
	return tea.Batch(m.spin.Tick, m.background(func() tea.Msg {
		warns, err := a.Prepare(m.ctx)
		return prepMsg{warns: warns, err: err}
	}))
}

// background runs f as a command that Run waits for.
func (m *model) background(f func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if !m.tasks.start() {
			return nil
		}
		defer m.tasks.done()
		return f()
	}
}

// tasks counts goroutines that must end before Run returns. Once wait is called, start refuses
// new ones: Bubble Tea may run a command after the program has quit, and a WaitGroup must not
// grow during Wait.
type tasks struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
}

func (t *tasks) start() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.wg.Add(1)
	return true
}

func (t *tasks) done() { t.wg.Done() }

func (t *tasks) wait() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.wg.Wait()
}

// sync copies what the view shows out of the agent. Call it only while no run or switch is in
// flight, since those mutate the agent from another goroutine.
func (m *model) sync() {
	ag := m.app.Agent
	m.provider, m.modelID, m.effort, m.window = m.app.ProviderID, ag.Model, ag.Effort, ag.Context
}

func (m *model) out(lines ...string) { m.lines = append(m.lines, lines...) }

// flush prints what this update produced as one block, so its lines stay in order.
func (m *model) flush() tea.Cmd {
	if len(m.lines) == 0 {
		return nil
	}
	text := strings.Join(m.lines, "\n")
	m.lines = nil
	return tea.Println(text)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 20)
		m.md.width = m.width
		m.input.SetWidth(m.width)
		return m, nil

	case prepMsg:
		m.busy = ""
		m.sync()
		m.banner(msg.warns)
		if msg.err != nil {
			m.out(m.st.Error.Render("error: " + msg.err.Error()))
			m.out(m.st.Dim.Render("pick a model with /model, or a provider with /provider"))
		}
		if dir, files := m.app.Trust(); dir != "" {
			m.trust, m.busy = dir, "waiting for an answer"
			for _, l := range TrustLines(m.st, dir, files) {
				m.out(l)
			}
		}
		return m, tea.Sequence(m.flush(), m.next())

	case switchedMsg:
		m.busy = ""
		m.sync()
		if msg.err != nil {
			m.out(m.st.Error.Render("error: " + msg.err.Error()))
		} else {
			m.out(m.st.Dim.Render("using ") + m.st.Model.Render(m.provider+"/"+shortModel(m.modelID)) + m.st.Dim.Render(m.windowNote()))
		}
		return m, tea.Sequence(m.flush(), m.next())

	case modelsMsg:
		m.busy = ""
		return m, tea.Sequence(m.showModels(msg), m.flush(), m.next())

	case eventMsg:
		m.event(msg.ev)
		return m, tea.Sequence(m.flush(), listen(m.events))

	case approvalMsg:
		if m.allowed(msg.call.Name, msg.why) {
			msg.reply <- true
		} else {
			m.approval = &msg
			m.phase = "waiting for approval"
			for _, l := range ApprovalLines(m.st, msg.label, msg.preview, m.width) {
				m.out(l)
			}
		}
		return m, tea.Sequence(m.flush(), listen(m.events))

	case doneMsg:
		m.finish(msg)
		return m, tea.Sequence(m.flush(), m.next())

	case spinner.TickMsg:
		if !m.running && m.busy == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		if m.trust != "" {
			return m, m.trustKey(msg)
		}
		if m.approval != nil {
			return m, m.approvalKey(msg)
		}
		if m.picker != nil {
			return m, m.pickerKey(msg)
		}
		if cmd, handled := m.key(msg); handled {
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// key handles the REPL's own bindings; anything else goes to the input.
func (m *model) key(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if msg.String() != "tab" {
		m.tabSeed = ""
	}
	switch msg.String() {
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return nil, true
		}
		m.input.Reset()
		m.input.Placeholder = "" // hint only before the first entry
		m.hist.Add(text)
		m.histPos, m.draft = len(m.hist.Entries), ""
		if strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "//") && !strings.Contains(strings.Fields(text)[0][1:], "/") {
			return tea.Sequence(m.command(text), m.flush()), true
		}
		m.queue = append(m.queue, text)
		return tea.Sequence(m.flush(), m.next()), true
	case "esc":
		if m.running {
			m.stopped = true
			m.cancel()
		}
		return nil, true
	case "ctrl+c":
		switch {
		case m.running:
			m.stopped = true
			m.cancel()
		case m.input.Value() != "":
			m.input.Reset()
		default:
			return tea.Quit, true
		}
		return nil, true
	case "ctrl+d":
		if m.input.Value() == "" && !m.running {
			return tea.Quit, true
		}
	case "up", "ctrl+p":
		if !strings.Contains(m.input.Value(), "\n") && m.histPos > 0 {
			if m.histPos == len(m.hist.Entries) {
				m.draft = m.input.Value()
			}
			m.histPos--
			m.input.SetValue(m.hist.Entries[m.histPos])
			return nil, true
		}
	case "down", "ctrl+n":
		if !strings.Contains(m.input.Value(), "\n") && m.histPos < len(m.hist.Entries) {
			m.histPos++
			if m.histPos == len(m.hist.Entries) {
				m.input.SetValue(m.draft)
			} else {
				m.input.SetValue(m.hist.Entries[m.histPos])
			}
			return nil, true
		}
	case "tab":
		return m.complete(), true
	case "ctrl+r":
		m.searchHistory()
		return nil, true
	}
	return nil, false
}

// next starts the queued prompt when nothing else is running.
func (m *model) next() tea.Cmd {
	if m.running || m.busy != "" || len(m.queue) == 0 {
		return nil
	}
	text := m.queue[0]
	m.queue = m.queue[1:]
	m.out("")
	for i, l := range strings.Split(text, "\n") {
		p := "  "
		if i == 0 {
			p = "> "
		}
		m.out(m.st.Accent.Bold(true).Render(p) + m.st.User.Render(l))
	}
	return tea.Sequence(m.flush(), m.start(text))
}

func (m *model) start(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.running, m.stopped, m.started, m.phase = cancel, true, false, time.Now(), "waiting"
	m.reply = ""
	m.prompts++
	ch := make(chan tea.Msg, eventBuffer)
	m.events = ch
	// Set before the run starts, so the agent goroutine only reads it.
	m.app.SetPermissions("", askVia(ch, m.app.Preview, m.allowed))
	ag := m.app.Agent
	// Sends watch m.ctx, not ctx: Esc cancels ctx, and doneMsg must still arrive.
	send := func(msg tea.Msg) {
		select {
		case ch <- msg:
		case <-m.ctx.Done():
		}
	}
	if !m.tasks.start() {
		return nil
	}
	go func() {
		defer m.tasks.done()
		res, err := ag.Run(ctx, text, func(e agent.Event) { send(eventMsg{e}) })
		send(doneMsg{res, err})
		close(ch)
	}()
	return tea.Batch(listen(ch), m.spin.Tick)
}

// eventBuffer is how many events a run may send ahead of the UI.
var eventBuffer = 256

// askVia sends approval questions to the REPL through the run's event channel and waits for
// the answer, or for the run to be cancelled. The preview is made here, off the UI goroutine,
// since it may read files, and skipped for a call that allowed says "a" already covers.
func askVia(ch chan<- tea.Msg, preview func(tool.Tool, llm.ToolCall) string, allowed func(string, permission.Reason) bool) permission.AskFunc {
	return func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string, why permission.Reason) (bool, error) {
		var p string
		if !allowed(call.Name, why) {
			p = preview(t, call)
		}
		reply := make(chan bool, 1)
		select {
		case ch <- approvalMsg{call: call, label: label, why: why, preview: p, reply: reply}:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		select {
		case ok := <-reply:
			return ok, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func (m *model) approvalKey(msg tea.KeyPressMsg) tea.Cmd {
	q := m.approval
	answer := func(ok bool) {
		m.approval, m.phase = nil, q.label
		q.reply <- ok
	}
	switch msg.String() {
	case "y":
		answer(true)
	case "a":
		if !q.why.Lasting() {
			break
		}
		m.always[allowance{q.call.Name, q.why.Kind}] = true
		m.out(m.st.Dim.Render("  " + scope(q.call.Name, q.why.Kind) + " allowed until /clear or /permissions"))
		answer(true)
	case "n", "esc":
		answer(false)
	case "ctrl+c":
		answer(false)
		m.stopped = true
		m.cancel()
	}
	return m.flush()
}

// allowed reports whether an earlier "a" covers a call. The agent goroutine calls it too. That is
// safe because always changes only while the agent waits for an approval or no run is active.
func (m *model) allowed(name string, why permission.Reason) bool {
	return why.Lasting() && m.always[allowance{name, why.Kind}]
}

// allowance is what "a" at an approval prompt remembers.
type allowance struct {
	tool string
	kind permission.Kind
}

// scope names the calls an allowance covers, such as "edit outside the working directory".
func scope(name string, k permission.Kind) string {
	switch k {
	case permission.Outside:
		return name + " outside the working directory"
	case permission.Host:
		return name + " to unlisted hosts"
	}
	return name
}

func (m *model) trustKey(msg tea.KeyPressMsg) tea.Cmd {
	var ok bool
	switch msg.String() {
	case "y":
		ok = true
	case "n", "esc":
	case "ctrl+c", "ctrl+d":
		return tea.Quit
	default:
		return nil
	}
	if err := m.app.SetTrust(m.trust, ok); err != nil {
		m.out(m.st.Warn.Render("warning: " + err.Error()))
	}
	m.trust, m.busy = "", ""
	if ok {
		m.out(m.st.Dim.Render("trusted"))
	} else {
		m.out(m.st.Dim.Render(distrustNote))
	}
	return tea.Sequence(m.flush(), m.next())
}

const distrustNote = "not trusted, so permissions are ask; /permissions auto changes that for this session"

func listen(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) event(ev agent.Event) {
	switch e := ev.(type) {
	case agent.Text:
		m.phase = "writing"
		m.partial += e.Text
		m.reply += e.Text
		for {
			i := strings.IndexByte(m.partial, '\n')
			if i < 0 {
				break
			}
			m.out(m.md.add(text(m.partial[:i]))...)
			m.partial = m.partial[i+1:]
		}
	case agent.Reasoning:
		m.phase = "thinking"
		if !m.showThink {
			return
		}
		m.thinking += e.Text
		for {
			i := strings.IndexByte(m.thinking, '\n')
			if i < 0 {
				break
			}
			if l := m.thinking[:i]; strings.TrimSpace(l) != "" {
				m.out(m.st.Dim.Italic(true).Render("  " + text(l)))
			}
			m.thinking = m.thinking[i+1:]
		}
	case agent.Retry:
		m.endText()
		m.phase = fmt.Sprintf("retry %d: %s", e.Attempt, oneLine(e.Reason))
		m.out(m.st.Warn.Render(RetryLine(e)))
	case agent.ToolStart:
		m.endText()
		m.phase = "preparing " + e.Name
	case agent.ToolCall:
		m.phase = e.Label
	case agent.ToolResult:
		m.out(ToolLine(m.st, e, m.width))
		m.phase = "waiting"
	case agent.Response:
		m.endText()
		if strings.TrimSpace(m.reply) != "" {
			m.lastReply = m.reply
		}
		m.reply = ""
		m.app.Remember()
		m.session.Add(e.Usage)
		m.used = e.Usage.Input + e.Usage.Output
	}
}

// endText flushes a partial line and closes the markdown state at the end of a response.
func (m *model) endText() {
	if m.partial != "" {
		m.out(m.md.add(text(m.partial))...)
		m.partial = ""
	}
	m.out(m.md.flush()...)
	if m.thinking != "" && m.showThink {
		m.out(m.st.Dim.Italic(true).Render("  " + text(m.thinking)))
	}
	m.thinking = ""
	m.md.reset()
}

func (m *model) finish(d doneMsg) {
	m.endText()
	m.running, m.phase = false, ""
	m.cancel()
	m.sync()
	switch {
	case d.err != nil && (m.stopped || errors.Is(d.err, context.Canceled)):
		m.out(m.st.Warn.Render("cancelled"))
		m.queue = nil
	case agent.ContextFull(d.err):
		m.out(m.st.Error.Render("the conversation no longer fits the context window; /clear starts a new one"))
		m.queue = nil
	case d.err != nil:
		m.out(m.st.Error.Render("error: " + d.err.Error()))
	case d.res.Stop == llm.StopMaxTokens:
		m.out(m.st.Warn.Render(TruncatedNote))
	}
	if d.res.Turns > 0 {
		m.out(m.st.Dim.Render(usageLine(m.used, m.window, d.res.Usage, m.session)))
	}
}

func (m *model) banner(warns []error) {
	m.out(m.st.Banner.Render("gilda "+m.opts.Version) + "  " + m.st.Model.Render(m.provider+"/"+shortModel(m.modelID)) +
		m.st.Dim.Render(m.windowNote()+"  "+shortPath(m.app.Root())+"  permissions: "+string(m.app.Mode())))
	for _, p := range prompt.AgentsFiles(m.app.Root(), m.app.ConfigDir()) {
		m.out(m.st.Dim.Render("  instructions: " + shortPath(p)))
	}
	if m.app.Distrusted() {
		m.out(m.st.Dim.Render("  " + distrustNote))
	}
	for _, w := range warns {
		m.out(m.st.Warn.Render("warning: " + w.Error()))
	}
}

func (m *model) windowNote() string {
	if m.window > 0 {
		return " (" + shortTokens(m.window) + " context)"
	}
	return ""
}

func (m *model) sessionLine() string {
	u := m.session
	return fmt.Sprintf("session: %d prompts | in %s (%s cached) | out %s | %s",
		m.prompts, shortTokens(u.Input), shortTokens(u.CacheRead), shortTokens(u.Output), money(u.Cost, u.Estimated))
}

func (m *model) View() tea.View {
	var b strings.Builder
	w := m.width
	if m.running && m.showThink && m.thinking != "" {
		b.WriteString(ansi.Hardwrap(m.st.Dim.Italic(true).Render("  "+text(m.thinking)), w, true) + "\n")
	}
	if m.running && (m.partial != "" || len(m.md.table) > 0) {
		md := m.md // a copy, so the unfinished line does not change the state
		var ls []string
		if m.partial != "" {
			ls = md.add(text(m.partial))
		}
		ls = append(ls, md.flush()...)
		b.WriteString(ansi.Hardwrap(strings.Join(ls, "\n"), w, true) + "\n")
	}
	if m.picker != nil {
		b.WriteString(m.picker.view(m.st, w))
	}
	if m.trust != "" {
		b.WriteString(m.st.Warn.Bold(true).Render("trust this directory?") +
			m.st.Dim.Render("  y yes | n no, use ask mode | Ctrl-C quit") + "\n")
	}
	if q := m.approval; q != nil {
		keys := "  y yes | n no | Esc no"
		if q.why.Lasting() {
			keys = "  y yes | n no | a always for " + scope(q.call.Name, q.why.Kind) + " | Esc no"
		}
		b.WriteString(m.st.Warn.Bold(true).Render("allow the "+q.call.Name+" call above?") + m.st.Dim.Render(keys) + "\n")
	}
	for _, q := range m.queue {
		b.WriteString(m.st.Dim.Render(ansi.Truncate("queued: "+oneLine(q), w, "...")) + "\n")
	}
	b.WriteString(m.st.Dim.Render(strings.Repeat("-", w)) + "\n")
	b.WriteString(m.input.View() + "\n")
	b.WriteString(m.statusBar())
	return tea.NewView(b.String())
}

func (m *model) statusBar() string {
	var left string
	switch {
	case m.running:
		el := time.Since(m.started).Truncate(time.Second)
		left = m.st.BarBusy.Render(m.spin.View()+" "+el.String()) + m.st.BarLeft.Render(m.phase)
	case m.busy != "":
		left = m.st.BarBusy.Render(m.spin.View() + " " + m.busy)
	default:
		left = m.st.BarLeft.Render(shortPath(m.app.Root()))
	}
	if n := len(m.queue); n > 0 {
		left += m.st.BarLeft.Render(fmt.Sprintf("%d queued", n))
	}
	// A narrow terminal drops cost, effort and ctx, in that order, then cuts the model id, then
	// drops the mode. The mode goes last, since it says what runs unasked.
	type seg struct {
		s    string
		drop int
	}
	var segs []seg
	if m.effort != "" {
		segs = append(segs, seg{m.st.BarLeft.Render(m.effort), 3})
	}
	if mode := m.app.Mode(); mode != permission.Auto {
		segs = append(segs, seg{m.st.BarBusy.Render(string(mode)), 1})
	}
	ctx := "ctx " + shortTokens(m.used)
	if m.window > 0 {
		ctx = fmt.Sprintf("ctx %d%%", m.used*100/m.window)
	}
	ctxStyle := m.st.BarCtx
	if m.window > 0 && m.used*100/m.window >= ctxWarn {
		ctx, ctxStyle = ctx+"!", m.st.BarWarn
	}
	segs = append(segs, seg{ctxStyle.Render(ctx), 2})
	if m.session.Cost != nil {
		segs = append(segs, seg{m.st.BarCost.Render(money(m.session.Cost, m.session.Estimated)), 4})
	}
	id := m.provider + "/" + shortModel(m.modelID)
	// room keeps a few columns of the left side, so a run's spinner stays visible.
	room := m.width - min(lipgloss.Width(left), barLeftMin)
	rest := func() int {
		n := 0
		for _, g := range segs {
			n += lipgloss.Width(g.s)
		}
		return n
	}
	for len(segs) > 0 {
		i := 0
		for j, g := range segs {
			if g.drop > segs[i].drop {
				i = j
			}
		}
		need := lipgloss.Width(id) + 2 + rest()
		if segs[i].drop == 1 {
			need = barModelMin + 2 + rest() // the model id is cut before the mode goes
		}
		if need <= room {
			break
		}
		segs = append(segs[:i], segs[i+1:]...)
	}
	right := m.st.BarModel.Render(ansi.Truncate(id, max(room-2-rest(), 1), "~"))
	for _, g := range segs {
		right += g.s
	}
	right = ansi.Truncate(right, max(room, 0), "")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = ansi.Truncate(left, max(m.width-lipgloss.Width(right)-1, 0), "")
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	return left + m.st.BarFill.Render(strings.Repeat(" ", max(gap, 0))) + right
}

// barLeftMin is the width of the status bar's left side that the right side cannot take.
const barLeftMin = 12

// ctxWarn is the context use, in percent, that the status bar flags with "!" and colour.
const ctxWarn = 85

// barModelMin is how much of the model id the status bar keeps before it drops the mode.
const barModelMin = 12

// shortModel drops the directory from a model id that is a file path, as llama-server reports.
func shortModel(id string) string {
	if strings.HasPrefix(id, "/") {
		return filepath.Base(id)
	}
	return id
}
