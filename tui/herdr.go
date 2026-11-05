package tui

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/shakfu/gilda/permission"
)

// herdr reports the REPL's state to Herdr, the terminal multiplexer it may run in:
// https://herdr.dev/tui/docs/add-herdr-support/. Reports run in the background and failures
// are ignored. A report waiting to be sent is replaced by a newer one, so only the latest
// state is sent. A nil *herdr does nothing.
type herdr struct {
	bin, pane string
	// resumable is set when "gilda" is on PATH, as Herdr's resume command requires.
	resumable bool
	run       func(ctx context.Context, bin string, args ...string) error

	mu   sync.Mutex
	cond *sync.Cond
	seq  int64
	next []string // the report waiting to be sent
	busy bool     // a goroutine is sending reports
}

const (
	herdrSource  = "gilda"
	herdrTimeout = 2 * time.Second
	// herdrMessage caps the length of a blocked report's message.
	herdrMessage = 200
)

// newHerdr returns a reporter when the REPL runs in a Herdr pane, else nil.
func newHerdr(getenv func(string) string) *herdr {
	bin, pane := getenv("HERDR_BIN_PATH"), getenv("HERDR_PANE_ID")
	if getenv("HERDR_ENV") != "1" || bin == "" || pane == "" || getenv("HERDR_SOCKET_PATH") == "" {
		return nil
	}
	_, err := exec.LookPath("gilda")
	h := &herdr{bin: bin, pane: pane, resumable: err == nil, run: func(ctx context.Context, bin string, args ...string) error {
		return exec.CommandContext(ctx, bin, args...).Run()
	}}
	h.cond = sync.NewCond(&h.mu)
	return h
}

// resumeArgv is the command that continues session id after a Herdr restart, or nil when Herdr
// would refuse it. An auto mode is left out, so a resumed REPL asks the trust question again.
func (h *herdr) resumeArgv(root string, mode permission.Mode, id string) []string {
	if h == nil || !h.resumable || id == "" {
		return nil
	}
	argv := []string{"gilda", "--resume", id, "-C", root}
	if mode != permission.Auto {
		argv = append(argv, "--permissions", string(mode))
	}
	for _, a := range argv {
		if strings.ContainsRune(a, '\'') || strings.ContainsFunc(a, unicode.IsControl) {
			return nil
		}
	}
	if len(strings.Join(argv, " ")) > 8<<10 {
		return nil
	}
	return argv
}

// report sends state, one of idle, working and blocked. message says why it is blocked; id and
// resume name the session and the command that continues it, and may be empty.
func (h *herdr) report(state, message, id string, resume []string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// The pane comes first: herdr 0.9.5's release-agent fails with "unknown option" when it
	// follows the options, though its usage puts it last.
	args := []string{"pane", "report-agent", h.pane, "--source", herdrSource, "--agent", "gilda", "--state", state, "--seq", h.nextSeq()}
	if message != "" {
		args = append(args, "--message", ansi.Truncate(oneLine(message), herdrMessage, "..."))
	}
	if id != "" && len(resume) > 0 {
		args = append(args, "--agent-session-id", id, "--")
		args = append(args, resume...)
	}
	h.next = args
	if !h.busy {
		h.busy = true
		go h.loop()
	}
}

func (h *herdr) loop() {
	h.mu.Lock()
	for h.next != nil {
		args := h.next
		h.next = nil
		h.mu.Unlock()
		h.exec(args)
		h.mu.Lock()
	}
	h.busy = false
	h.cond.Broadcast()
	h.mu.Unlock()
}

// release tells Herdr the user has quit. It drops a report not yet sent, waits for the one in
// flight, then releases the pane before returning.
func (h *herdr) release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.next = nil
	h.wait()
	args := []string{"pane", "release-agent", h.pane, "--source", herdrSource, "--agent", "gilda", "--seq", h.nextSeq()}
	h.mu.Unlock()
	h.exec(args)
}

// wait blocks until no report is in flight. Call it with h.mu held.
func (h *herdr) wait() {
	for h.busy {
		h.cond.Wait()
	}
}

// nextSeq returns a sequence number above the last, as Herdr ignores one that is not. It is a
// timestamp, so a new gilda in the same pane continues above the previous one. Call it with
// h.mu held.
func (h *herdr) nextSeq() string {
	h.seq = max(h.seq+1, time.Now().UnixMilli())
	return strconv.FormatInt(h.seq, 10)
}

func (h *herdr) exec(args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	_ = h.run(ctx, h.bin, args...)
}
