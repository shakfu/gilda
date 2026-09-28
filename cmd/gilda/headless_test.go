package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shakfu/gilda/tui"
)

// The -p prompt shows control characters in the call as escapes, so a command cannot rewrite
// what the user reads before answering.
func TestTTYAskShowsTheCallEscaped(t *testing.T) {
	var out bytes.Buffer
	tty := struct {
		io.Reader
		io.Writer
	}{strings.NewReader("y\n"), &out}
	lines := tui.ApprovalLines(tui.NewStyles(), "$ echo safe\r\x1b]0;title\x07rm -rf x", "", 0)
	ok, err := askOn(context.Background(), tty, &out, lines, "allow this bash call?")
	if !ok || err != nil {
		t.Fatalf("got %v %v", ok, err)
	}
	s := out.String()
	if strings.Contains(s, "\x1b]") || strings.Contains(s, "\r") {
		t.Errorf("raw control characters reached the terminal: %q", s)
	}
	if !strings.Contains(s, `$ echo safe\x0d\x1b]0;title\x07rm -rf x`) || !strings.Contains(s, "allow this bash call?") {
		t.Errorf("prompt: %q", s)
	}
}
