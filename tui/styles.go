package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/price"
)

// Styles is the palette. Output is ASCII; colour carries the structure.
type Styles struct {
	Banner, Dim, Accent, Model, Error, Warn, OK, Cost    lipgloss.Style
	User                                                 lipgloss.Style
	Tool                                                 map[string]lipgloss.Style
	Heading, Bold, Italic, Code, CodeBlock, Link, Quote  lipgloss.Style
	BarLeft, BarModel, BarCtx, BarCost, BarFill, BarBusy lipgloss.Style
	BarWarn                                              lipgloss.Style
	Selected, Match                                      lipgloss.Style
}

// Writer downsamples colour for w, the way the REPL's renderer does for the terminal: a pipe
// gets plain text. With color off a terminal keeps bold and italic and drops colour.
func Writer(w io.Writer, color bool) io.Writer {
	cw := colorprofile.NewWriter(w, os.Environ())
	if !color && cw.Profile > colorprofile.Ascii {
		cw.Profile = colorprofile.Ascii
	}
	return cw
}

// NewStyles builds the palette. Colour is downsampled where it is written, not here.
func NewStyles() Styles {
	s := func(fg string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)) }
	bar := func(fg, bg string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Padding(0, 1)
	}
	return Styles{
		Banner: s("81").Bold(true),
		Dim:    s("245"),
		Accent: s("81"),
		Model:  s("177"),
		Error:  s("203"),
		Warn:   s("221"),
		OK:     s("114"),
		Cost:   s("150"),
		User:   s("252").Bold(true),
		Tool: map[string]lipgloss.Style{
			"read":  s("75").Bold(true),
			"write": s("114").Bold(true),
			"edit":  s("221").Bold(true),
			"bash":  s("209").Bold(true),
		},
		Heading:   s("81").Bold(true),
		Bold:      lipgloss.NewStyle().Bold(true),
		Italic:    lipgloss.NewStyle().Italic(true),
		Code:      s("216"),
		CodeBlock: s("180"),
		Link:      s("75").Underline(true),
		Quote:     s("245").Italic(true),
		BarLeft:   bar("252", "237"),
		BarModel:  bar("231", "97").Bold(true),
		BarCtx:    bar("231", "31"),
		BarCost:   bar("16", "150"),
		BarFill:   lipgloss.NewStyle().Background(lipgloss.Color("236")),
		BarBusy:   bar("16", "221").Bold(true),
		BarWarn:   bar("231", "160").Bold(true),
		Selected:  s("231").Background(lipgloss.Color("97")).Bold(true),
		Match:     s("252"),
	}
}

// toolPrefix marks a tool line, as in myra, so it stands apart from the answer when the
// transcript is read without colour.
const toolPrefix = "[tool] "

// ToolLine formats a finished call as one line: "[tool] ", its label, then the outcome. width
// 0 means no limit. When the line is too wide the label is cut first, so the outcome stays.
func ToolLine(st Styles, r agent.ToolResult, width int) string {
	name := r.Call.Name
	style, ok := st.Tool[name]
	if !ok {
		style = st.Accent.Bold(true)
	}
	label := r.Label
	head, rest, _ := strings.Cut(label, " ")
	if name == "bash" {
		head, rest = "$", strings.TrimPrefix(label, "$ ")
	}
	var outcome string
	switch {
	case r.Err != nil:
		outcome = st.Error.Render("error: " + oneLine(ansi.Strip(firstLine(r.Err.Error()))))
	case r.Result.Failed:
		outcome = st.Warn.Render(r.Result.Summary)
	default:
		outcome = st.Dim.Render(r.Result.Summary)
	}
	rest = oneLine(ansi.Strip(rest))
	build := func(rest string) string {
		line := st.Dim.Render(toolPrefix) + style.Render(head)
		if rest != "" {
			line += " " + st.Dim.Render(rest)
		}
		return line + st.Dim.Render(" -> ") + outcome
	}
	line := build(rest)
	if width > 0 && ansi.StringWidth(line) > width {
		fixed := len(toolPrefix) + ansi.StringWidth(head) + len(" ") + len(" -> ") + ansi.StringWidth(outcome)
		if room := width - fixed; room > 8 && rest != "" {
			line = build(ansi.Truncate(rest, room, "..."))
		}
		if r.Err == nil {
			line = ansi.Truncate(line, width, "...")
		}
	}
	return line
}

// ApprovalLines prints the call awaiting approval in full, then its preview, wrapped to width
// (0 for none), so what the user approves is exactly what runs. The live prompt below has room
// for one line.
func ApprovalLines(st Styles, label, preview string, width int) []string {
	lines := []string{st.Warn.Bold(true).Render("approve:")}
	for _, l := range strings.Split(Visible(label), "\n") {
		lines = append(lines, ansi.Hardwrap("  "+l, width, true))
	}
	if preview == "" {
		return lines
	}
	for _, l := range strings.Split(strings.TrimSuffix(preview, "\n"), "\n") {
		// A CRLF file would otherwise end every line with \x0d.
		l = Visible(strings.TrimSuffix(l, "\r"))
		style := lipgloss.NewStyle()
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
			style = st.Dim
		case strings.HasPrefix(l, "@@"):
			style = st.Accent
		case strings.HasPrefix(l, "+"):
			style = st.OK
		case strings.HasPrefix(l, "-"):
			style = st.Error
		}
		lines = append(lines, ansi.Hardwrap(style.Render("  "+l), width, true))
	}
	return lines
}

// Visible shows control characters other than newline and tab as escapes, and so bidirectional
// controls and zero-width characters, which make text display differently from what runs. See
// CVE-2021-42574. Stripping them would hide bytes from the user that the tool still receives.
func Visible(s string) string { return escape(s, true) }

// text makes model output safe to print: escape sequences could set the window title, write
// the clipboard or move the cursor. Joiners and direction marks stay, since scripts such as
// Persian use them in ordinary prose.
func text(s string) string { return escape(strings.TrimSuffix(s, "\r"), false) }

func escape(s string, strict bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r < 0xa0,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			strict && (r >= 0x200b && r <= 0x200f || r >= 0x2060 && r <= 0x2064 || r == 0x061c || r == 0xfeff):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TrustLines explain the question asked before a checkout's AGENTS.md files may instruct an
// agent in auto mode.
func TrustLines(st Styles, dir string, files []string) []string {
	lines := []string{st.Warn.Bold(true).Render(Visible(dir) + " has instructions for the agent:")}
	for _, f := range files {
		lines = append(lines, "  "+Visible(f))
	}
	return append(lines, st.Dim.Render("auto mode runs bash and writes inside it without asking; "+
		"untrusted, gilda asks before each (ask mode)"))
}

// TruncatedNote follows an answer cut off at the output limit.
const TruncatedNote = "the answer was cut off at the output limit; raise --max-tokens or max_tokens in settings.toml"

// RetryLine describes a retry, such as "[retry] 2 after 503 Service Unavailable".
func RetryLine(r agent.Retry) string {
	line := fmt.Sprintf("[retry] %d", r.Attempt)
	if r.Reason != "" {
		line += " after " + oneLine(r.Reason)
	}
	return line
}

// ElidedLine reports old tool results replaced by stubs to free context.
func ElidedLine(e agent.Elided) string {
	return fmt.Sprintf("[context] elided %d old tool results (%d KiB) to free context", e.Results, (e.Bytes+1023)/1024)
}

// UsageLine summarises a prompt: context used, tokens in and out, cost.
func UsageLine(a *app.App, u llm.Usage) string {
	return usageLine(a.Agent.Used, a.Agent.Context, u, a.Agent.Usage)
}

func usageLine(used, window int64, u, session llm.Usage) string {
	var parts []string
	if window > 0 {
		parts = append(parts, fmt.Sprintf("ctx %s/%s (%d%%)", price.Short(used), price.Short(window), used*100/window))
	} else {
		parts = append(parts, "ctx "+price.Short(used))
	}
	in := "in " + price.Short(u.Input)
	if u.CacheRead > 0 {
		in += fmt.Sprintf(" (%s cached)", price.Short(u.CacheRead))
	}
	parts = append(parts, in, "out "+price.Short(u.Output))
	if u.Cost != nil {
		c := money(u.Cost, u.Estimated)
		if session.Cost != nil && *session.Cost != *u.Cost {
			c += " (session " + money(session.Cost, session.Estimated) + ")"
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, " | ")
}

// money formats USD, marking an estimate with ~.
func money(c *float64, estimated bool) string {
	if c == nil {
		return "$-"
	}
	s := fmt.Sprintf("$%.4f", *c)
	if *c >= 1 {
		s = fmt.Sprintf("$%.2f", *c)
	}
	if estimated {
		s = "~" + s
	}
	return s
}

// shortPath replaces the home directory with ~.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// oneLine keeps the first line of s and drops control characters, which would move the
// cursor in the inline REPL.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}
