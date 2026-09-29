package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// markdown styles assistant text one complete line at a time, so it can render a stream as it
// arrives. Only fences carry state between lines. Tables pass through unchanged: they are
// already aligned in a monospace terminal. A row wider than width loses its cell padding
// instead, since a wrapped aligned row is harder to read than an unaligned one.
type markdown struct {
	st      Styles
	width   int
	inFence bool
	fence   string
}

var (
	heading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bullet   = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	numbered = regexp.MustCompile(`^(\s*)(\d+[.)])\s+(.*)$`)
	rule     = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	fenceRe  = regexp.MustCompile("^\\s*(```+|~~~+)(.*)$")

	inlineCode = regexp.MustCompile("``\\s?(.+?)\\s?``|`([^`]+)`")
	task       = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italicRe   = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*]*)\*|(^|[^_\w])_([^_\s][^_]*)_`)
	linkRe     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

func (m *markdown) reset() { m.inFence, m.fence = false, "" }

func (m *markdown) line(s string) string {
	if f := fenceRe.FindStringSubmatch(s); f != nil {
		switch {
		case !m.inFence:
			m.inFence, m.fence = true, f[1]
			return m.st.Dim.Render(s)
		case strings.HasPrefix(f[1], m.fence) && strings.TrimSpace(f[2]) == "":
			m.reset()
			return m.st.Dim.Render(s)
		}
	}
	if m.inFence {
		return m.st.CodeBlock.Render(s)
	}
	switch {
	case heading.MatchString(s):
		h := heading.FindStringSubmatch(s)
		return m.st.Heading.Render(h[1] + " " + stripInline(h[2]))
	case rule.MatchString(s):
		return m.st.Dim.Render(strings.Repeat("-", 40))
	case strings.HasPrefix(s, ">"):
		return m.st.Dim.Render("| ") + m.st.Quote.Render(strings.TrimSpace(strings.TrimPrefix(s, ">")))
	}
	if strings.HasPrefix(strings.TrimSpace(s), "|") && m.width > 0 && ansi.StringWidth(s) > m.width {
		return compactRow(s)
	}
	if b := bullet.FindStringSubmatch(s); b != nil {
		if t := task.FindStringSubmatch(b[2]); t != nil {
			box := m.st.Dim.Render("[ ]")
			if t[1] != " " {
				box = m.st.OK.Render("[x]")
			}
			return b[1] + m.st.Accent.Render("-") + " " + box + " " + m.inline(t[2])
		}
		return b[1] + m.st.Accent.Render("-") + " " + m.inline(b[2])
	}
	if n := numbered.FindStringSubmatch(s); n != nil {
		return n[1] + m.st.Accent.Render(n[2]) + " " + m.inline(n[3])
	}
	return m.inline(s)
}

// inline styles code spans first and leaves their contents alone, then bold, italic and links
// in the text between them.
func (m *markdown) inline(s string) string {
	var b strings.Builder
	for {
		loc := inlineCode.FindStringSubmatchIndex(s)
		if loc == nil {
			b.WriteString(m.emphasis(s))
			return b.String()
		}
		b.WriteString(m.emphasis(s[:loc[0]]))
		if loc[2] >= 0 {
			b.WriteString(m.st.Code.Render(s[loc[2]:loc[3]]))
		} else {
			b.WriteString(m.st.Code.Render(s[loc[4]:loc[5]]))
		}
		s = s[loc[1]:]
	}
}

func (m *markdown) emphasis(s string) string {
	s = linkRe.ReplaceAllStringFunc(s, func(x string) string {
		g := linkRe.FindStringSubmatch(x)
		if g[1] == g[2] {
			return m.st.Link.Render(g[1])
		}
		return m.st.Link.Render(g[1]) + m.st.Dim.Render(" ("+g[2]+")")
	})
	s = boldRe.ReplaceAllStringFunc(s, func(x string) string {
		g := boldRe.FindStringSubmatch(x)
		return m.st.Bold.Render(g[1] + g[2])
	})
	return italicRe.ReplaceAllStringFunc(s, func(x string) string {
		g := italicRe.FindStringSubmatch(x)
		return g[1] + g[3] + m.st.Italic.Render(g[2]+g[4])
	})
}

// stripInline drops emphasis markers inside a heading, which is already bold.
func stripInline(s string) string {
	s = boldRe.ReplaceAllString(s, "$1$2")
	return inlineCode.ReplaceAllString(s, "$1$2")
}

// compactRow trims the padding around each cell of a table row. An escaped pipe stays in its
// cell.
func compactRow(s string) string {
	cells := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), `\|`, "\x00"), "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
		if strings.Trim(cells[i], ":-") == "" && strings.Contains(cells[i], "---") {
			cells[i] = strings.Replace(cells[i], strings.Trim(cells[i], ":"), "---", 1)
		}
	}
	return strings.ReplaceAll(strings.TrimSpace(strings.Join(cells, " | ")), "\x00", `\|`)
}
