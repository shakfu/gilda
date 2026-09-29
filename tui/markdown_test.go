package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/tool"
)

func TestMarkdownLines(t *testing.T) {
	md := markdown{st: NewStyles()}
	cases := []struct{ in, want string }{
		{"## Title **x**", "## Title x"},
		{"- item with `code`", "- item with code"},
		{"* star item", "- star item"},
		{"1. first", "1. first"},
		{"**bold** and *it* and _it_", "bold and it and it"},
		{"see [docs](https://x.y)", "see docs (https://x.y)"},
		{"> quoted", "| quoted"},
		{"---", "----------------------------------------"},
		{"a * b * c", "a * b * c"},
		{"snake_case_name", "snake_case_name"},
		{"`**not bold**`", "**not bold**"},
	}
	for _, c := range cases {
		if got := ansi.Strip(md.line(c.in)); got != c.want {
			t.Errorf("line(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFencesAreNotStyledInside(t *testing.T) {
	md := markdown{st: NewStyles()}
	lines := []string{"```go", "# not a heading", "- not a bullet", "```", "- bullet"}
	var got []string
	for _, l := range lines {
		got = append(got, ansi.Strip(md.line(l)))
	}
	want := []string{"```go", "# not a heading", "- not a bullet", "```", "- bullet"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: %q, want %q", i, got[i], want[i])
		}
	}
	if md.inFence {
		t.Fatal("fence left open")
	}
}

func TestUsageLine(t *testing.T) {
	c := 0.0123
	s := 0.05
	got := usageLine(1500, 200000, llmUsage(1000, 400, 50, &c, true), llmUsage(0, 0, 0, &s, true))
	want := "ctx 1.5k/200k (0%) | in 1k (400 cached) | out 50 | ~$0.0123 (session ~$0.0500)"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func llmUsage(in, cached, out int64, cost *float64, est bool) llm.Usage {
	return llm.Usage{Input: in, CacheRead: cached, Output: out, Cost: cost, Estimated: est}
}

func TestToolLine(t *testing.T) {
	st := NewStyles()
	res := func(name, label, summary string, err error) agent.ToolResult {
		return agent.ToolResult{Call: llm.ToolCall{Name: name}, Label: label, Result: tool.Result{Summary: summary}, Err: err}
	}
	cases := []struct {
		r     agent.ToolResult
		width int
		want  string
	}{
		{res("read", "read main.go:1-80", "80 lines", nil), 0, "[tool] read main.go:1-80 -> 80 lines"},
		{res("bash", "$ go test ./...", "exit 1: FAIL", nil), 0, "[tool] $ go test ./... -> exit 1: FAIL"},
		{res("deploy", "deploy", "done", nil), 0, "[tool] deploy -> done"},
		{res("bash", "$ sed -n '1,300p' agent/agent.go && sed -n '1,260p' agent/record.go", "310 lines", nil), 50,
			"[tool] $ sed -n '1,300p' agent/age... -> 310 lines"},
		{res("write", "write /etc/x", "", errors.New("refused: auto mode asks before write outside /r")), 30,
			"[tool] write /etc/x -> error: refused: auto mode asks before write outside /r"},
	}
	for _, c := range cases {
		got := ansi.Strip(ToolLine(st, c.r, c.width))
		if got != c.want {
			t.Errorf("got  %q\nwant %q", got, c.want)
		}
		if c.width > 0 && c.r.Err == nil && ansi.StringWidth(got) > c.width {
			t.Errorf("%q is wider than %d", got, c.width)
		}
	}
}

// The approval prompt once cut the command to 20 columns at width 80, hiding a destructive
// suffix. Every character of the call must be shown, and none may act on the terminal.
func TestApprovalShowsTheWholeCall(t *testing.T) {
	st := NewStyles()
	label := "$ echo harmless-looking-prefix; rm -rf important-directory\ncat <<EOF\n\x1b[2Khidden\nEOF"
	got := ansi.Strip(strings.Join(ApprovalLines(st, label, "", 30), "\n"))
	joined := strings.ReplaceAll(got, "\n", "")
	for _, want := range []string{"rm -rf important-directory", "cat <<EOF", `\x1b[2Khidden`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("escape reached the terminal: %q", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Errorf("line wider than 30: %q", l)
		}
	}
}

func TestApprovalColoursTheDiff(t *testing.T) {
	st := NewStyles()
	diff := "--- f\n+++ f\n@@ -1,2 +1,2 @@\n a\r\n-b\r\n+c\x1b[2K\r\n"
	got := ApprovalLines(st, "edit f", diff, 80)
	plain := ansi.Strip(strings.Join(got, "\n"))
	for _, want := range []string{"  -b", `  +c\x1b[2K`, "  @@ -1,2 +1,2 @@"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in\n%s", want, plain)
		}
	}
	if strings.Contains(plain, `\x0d`) || strings.HasSuffix(plain, "\n") {
		t.Errorf("CR or trailing blank line shown:\n%q", plain)
	}
}

func TestTablesAreAligned(t *testing.T) {
	md := markdown{st: NewStyles()}
	var got []string
	for _, l := range []string{"| a | long header |", "|:-:|--:|", "| `x` | 1 |", "| wide cell | a \\| b |", "after"} {
		for _, r := range md.add(l) {
			got = append(got, ansi.Strip(r))
		}
	}
	want := []string{
		"|     a     | long header |",
		"|-----------|-------------|",
		"|     x     |           1 |",
		"| wide cell |       a | b |",
		"after",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPipeLinesWithoutSeparatorPassThrough(t *testing.T) {
	md := markdown{st: NewStyles()}
	if out := md.add("| not a table"); out != nil {
		t.Fatalf("row not held: %q", out)
	}
	got := md.flush()
	if len(got) != 1 || ansi.Strip(got[0]) != "| not a table" {
		t.Errorf("got %q", got)
	}
	md.add("```")
	if out := md.add("| code |"); len(out) != 1 {
		t.Errorf("a pipe line inside a fence was held: %q", out)
	}
}
