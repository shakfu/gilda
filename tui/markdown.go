package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// markdown styles assistant text one complete line at a time, so it can render a stream as it
// arrives. Fences and tables carry state between lines. A table is held until it ends, since
// its column widths depend on every row.
type markdown struct {
	st      Styles
	inFence bool
	fence   string
	table   []string
}

var (
	heading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bullet   = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	numbered = regexp.MustCompile(`^(\s*)(\d+[.)])\s+(.*)$`)
	rule     = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	fenceRe  = regexp.MustCompile("^\\s*(```+|~~~+)(.*)$")

	inlineCode = regexp.MustCompile("`([^`]+)`")
	boldRe     = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italicRe   = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*]*)\*|(^|[^_\w])_([^_\s][^_]*)_`)
	linkRe     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

	tableRow = regexp.MustCompile(`^\s*\|`)
	tableSep = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
)

func (m *markdown) reset() { m.inFence, m.fence, m.table = false, "", nil }

// add renders one complete line. It returns nothing while a table is open, then the table
// with the line that ended it.
func (m *markdown) add(s string) []string {
	if !m.inFence && tableRow.MatchString(s) {
		m.table = append(m.table, s)
		return nil
	}
	return append(m.flush(), m.line(s))
}

// flush renders a held table: aligned if its second row is a separator, else line by line.
func (m *markdown) flush() []string {
	rows := m.table
	m.table = nil
	if len(rows) >= 2 && tableSep.MatchString(rows[1]) {
		return m.align(rows)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = m.line(r)
	}
	return out
}

func (m *markdown) align(rows []string) []string {
	var grid [][]string
	var widths []int
	for i, r := range rows {
		cs := cells(r)
		for j, c := range cs {
			switch i {
			case 0:
				cs[j] = m.st.Bold.Render(stripInline(c))
			case 1:
				continue
			default:
				cs[j] = m.inline(c)
			}
			if j == len(widths) {
				widths = append(widths, 0)
			}
			widths[j] = max(widths[j], ansi.StringWidth(cs[j]))
		}
		grid = append(grid, cs)
	}
	bar := m.st.Dim.Render("|")
	out := make([]string, len(grid))
	for i, cs := range grid {
		var b strings.Builder
		b.WriteString(bar)
		for j, w := range widths {
			if i == 1 {
				b.WriteString(m.st.Dim.Render(strings.Repeat("-", w+2) + "|"))
				continue
			}
			c := ""
			if j < len(cs) {
				c = cs[j]
			}
			b.WriteString(" " + pad(c, w, colAlign(grid[1], j)) + " " + bar)
		}
		out[i] = b.String()
	}
	return out
}

// colAlign reads column j's alignment from the separator row: 'l', 'c' or 'r'.
func colAlign(sep []string, j int) byte {
	if j >= len(sep) {
		return 'l'
	}
	l, r := strings.HasPrefix(sep[j], ":"), strings.HasSuffix(sep[j], ":")
	switch {
	case l && r:
		return 'c'
	case r:
		return 'r'
	}
	return 'l'
}

func pad(s string, w int, align byte) string {
	n := w - ansi.StringWidth(s)
	switch align {
	case 'r':
		return strings.Repeat(" ", n) + s
	case 'c':
		return strings.Repeat(" ", n/2) + s + strings.Repeat(" ", n-n/2)
	}
	return s + strings.Repeat(" ", n)
}

// cells splits a table row on unescaped pipes, dropping the outer ones.
func cells(row string) []string {
	row = strings.TrimPrefix(strings.TrimSpace(row), "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = row[:len(row)-1]
	}
	var out []string
	var b strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			b.WriteByte('|')
			i++
		case row[i] == '|':
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(row[i])
		}
	}
	return append(out, strings.TrimSpace(b.String()))
}

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
	if b := bullet.FindStringSubmatch(s); b != nil {
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
		b.WriteString(m.st.Code.Render(s[loc[2]:loc[3]]))
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
	return inlineCode.ReplaceAllString(s, "$1")
}
