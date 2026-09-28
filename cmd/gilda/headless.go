package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/tool"
	"github.com/shakfu/gilda/tui"
)

// headless answers one prompt. Text streams to stdout and everything else goes to stderr, so
// stdout is the answer. With asJSON, stdout carries one record per line instead.
func headless(parent context.Context, a *app.App, prompt string, asJSON, color bool) int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)
	go func() {
		select {
		case <-interrupt:
			cancel()
		case <-ctx.Done():
		}
	}()

	// A one-shot run asks on the terminal when it has one. --json output is read by a program,
	// so a call that needs approval is refused there.
	if !asJSON {
		a.SetPermissions("", ttyAsk(a.Preview, color))
	}

	if dir, files := a.Trust(); dir != "" {
		ok, answered := false, false
		if !asJSON {
			ok, answered = ttyTrust(ctx, dir, files, color)
		}
		if answered {
			if err := a.SetTrust(dir, ok); err != nil && !asJSON {
				fmt.Fprintln(os.Stderr, "gilda: warning:", err)
			}
		} else {
			// Not recorded: nobody answered.
			a.SetPermissions(permission.Ask, a.Ask())
			if !asJSON {
				fmt.Fprintf(os.Stderr, "gilda: %s is not trusted yet, so this run uses ask mode; "+
					"answer in the REPL, or pass --permissions auto\n", tui.Visible(dir))
			}
		}
	}

	warns, err := a.Prepare(ctx)
	if err != nil {
		if asJSON {
			return writeFailure(os.Stdout, err)
		}
		fmt.Fprintln(os.Stderr, "gilda:", err)
		return 1
	}
	for _, w := range warns {
		if !asJSON {
			fmt.Fprintln(os.Stderr, "gilda: warning:", w)
		}
	}

	diag, st := tui.Writer(os.Stderr, color), tui.NewStyles()
	var emit func(agent.Event)
	var j *jsonOut
	if asJSON {
		j = &jsonOut{w: os.Stdout}
		j.write(map[string]any{"type": "start", "provider": a.ProviderID, "model": a.Agent.Model,
			"session_id": a.Agent.SessionID, "context_window": a.Agent.Context})
		emit = j.event
	} else {
		emit = textEvents(os.Stdout, diag, st)
	}

	res, err := a.Agent.Run(ctx, prompt, func(e agent.Event) {
		if _, ok := e.(agent.Response); ok {
			a.Remember()
		}
		emit(e)
	})
	cancelled := errors.Is(err, context.Canceled) || ctx.Err() != nil

	if asJSON {
		j.result(a, res, err, cancelled)
	} else {
		if res.Turns > 0 {
			fmt.Fprintln(diag, st.Dim.Render(tui.UsageLine(a, res.Usage)))
		}
		if err != nil && !cancelled {
			fmt.Fprintln(diag, st.Error.Render("error: "+err.Error()))
		}
		if truncated(res, err) {
			fmt.Fprintln(diag, st.Warn.Render(tui.TruncatedNote))
		}
	}
	switch {
	case cancelled:
		return 130
	case err != nil:
		return 1
	}
	return 0
}

// textEvents prints each round-trip's text when it ends. A stream cut off partway is sent
// again, so text printed as it streamed would appear twice on stdout.
func textEvents(out, diag io.Writer, st tui.Styles) func(agent.Event) {
	var held strings.Builder
	return func(e agent.Event) {
		switch e := e.(type) {
		case agent.Text:
			held.WriteString(e.Text)
		case agent.Response:
			if s := held.String(); s != "" {
				fmt.Fprint(out, s)
				if !strings.HasSuffix(s, "\n") {
					fmt.Fprintln(out)
				}
			}
			held.Reset()
		case agent.ToolResult:
			fmt.Fprintln(diag, tui.ToolLine(st, e, 0))
		case agent.Retry:
			held.Reset()
			fmt.Fprintln(diag, st.Warn.Render(tui.RetryLine(e)))
		}
	}
}

// truncated reports whether a run ended without error on an answer cut at the output limit.
func truncated(res agent.Result, err error) bool {
	return err == nil && res.Stop == llm.StopMaxTokens
}

type jsonOut struct {
	w io.Writer
}

func (j *jsonOut) write(v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(j.w, "%s\n", data)
}

// event prints the records a caller needs to follow a run. Text arrives whole in turn records,
// so its deltas are not printed.
func (j *jsonOut) event(e agent.Event) {
	switch e.(type) {
	case agent.ToolCall, agent.ToolResult, agent.Retry, agent.Response:
		j.write(agent.Record(e))
	}
}

func (j *jsonOut) result(a *app.App, res agent.Result, err error, cancelled bool) {
	outcome, errText := "complete", any(nil)
	switch {
	case cancelled:
		outcome = "cancelled"
	case err != nil:
		outcome, errText = "error", err.Error()
	case truncated(res, err):
		outcome = "truncated"
	}
	j.write(map[string]any{
		"type": "result", "outcome": outcome, "text": res.Text, "error": errText,
		"provider": a.ProviderID, "model": a.Agent.Model, "turns": res.Turns, "permissions": a.Mode(),
		"usage": res.Usage, "context_used": a.Agent.Used, "context_window": a.Agent.Context,
	})
}

// writeFailure reports a setup error as the result record, so a caller parsing stdout sees it.
func writeFailure(w io.Writer, err error) int {
	(&jsonOut{w: w}).write(map[string]any{"type": "result", "outcome": "error", "error": err.Error(),
		"text": "", "turns": 0, "usage": llm.Usage{}})
	return 1
}

// ttyAsk asks on the controlling terminal, so the question reaches the user even when stdout
// and stderr are redirected. It shows the call and its preview as the REPL does. With no
// terminal the call is refused.
func ttyAsk(preview func(tool.Tool, llm.ToolCall) string, color bool) permission.AskFunc {
	return func(ctx context.Context, t tool.Tool, call llm.ToolCall, label string, _ permission.Reason) (bool, error) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return false, errors.New("refused: approval needs a terminal, and there is none")
		}
		defer tty.Close()
		lines := tui.ApprovalLines(tui.NewStyles(), label, preview(t, call), 0)
		return askOn(ctx, tty, tui.Writer(tty, color), lines, "allow this "+call.Name+" call?")
	}
}

// ttyTrust asks on the controlling terminal whether to trust dir; answered is false when there
// is no terminal.
func ttyTrust(ctx context.Context, dir string, files []string, color bool) (ok, answered bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, false
	}
	defer tty.Close()
	ok, err = askOn(ctx, tty, tui.Writer(tty, color), tui.TrustLines(tui.NewStyles(), dir, files), "trust it?")
	return ok, err == nil
}

// askOn prints lines to w, asks question, and reads a yes or no from tty.
func askOn(ctx context.Context, tty io.ReadWriter, w io.Writer, lines []string, question string) (bool, error) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintf(tty, "gilda: %s [y/N] ", question)
	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(tty).ReadString('\n')
		answer <- strings.TrimSpace(strings.ToLower(line))
	}()
	select {
	case a := <-answer:
		return a == "y" || a == "yes", nil
	case <-ctx.Done():
		fmt.Fprintln(tty)
		return false, ctx.Err()
	}
}
