//go:build unix

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shakfu/gilda/llm"
)

// loginShell matches a command wrapped in a login shell, such as `bash -lc '...'`. GPT models
// write this despite the tool description; a login profile can reorder PATH, so the command
// would run different programs than the one logged.
var loginShell = regexp.MustCompile(`^\s*(\S*/)?(ba|z|k)?sh\s+(-\w*l\w*|--login)\b`)

type Bash struct{ Env }

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func (b Bash) Spec() llm.ToolSpec {
	l := b.limits()
	return llm.ToolSpec{
		Name: "bash",
		Description: "Run a command with `bash -c` in the working directory and return its combined output " +
			"and exit status. Do not wrap it in another shell. stdin is empty. Background jobs must redirect their output.",
		Schema: schema([]string{"command"}, map[string]any{
			"command": prop("string", "The command."),
			"timeout": prop("integer", fmt.Sprintf("Seconds before the command is killed. Default %d, max %d.", l.BashTimeout, l.BashMaxTimeout)),
		}),
	}
}

func (Bash) Label(raw json.RawMessage) string {
	var a bashArgs
	_ = decode(raw, &a)
	return "$ " + a.Command
}

func (b Bash) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	var a bashArgs
	if err := decode(raw, &a); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return Result{}, fmt.Errorf("command is required")
	}
	if loginShell.MatchString(a.Command) {
		return Result{}, fmt.Errorf("command already runs under bash -c; pass the inner command without a login shell")
	}
	l := b.limits()
	timeout := l.BashTimeout
	if a.Timeout > 0 {
		timeout = min(a.Timeout, l.BashMaxTimeout)
	}

	// Room for the notes appended below, so the agent's cap does not cut the output twice.
	out := &capture{limit: l.OutputCap - 1024}
	cmd := exec.Command("bash", "-c", a.Command)
	cmd.Dir = b.Root
	if len(b.Hide) > 0 {
		// Environ, not os.Environ: it sets PWD to Dir.
		cmd.Env = slices.DeleteFunc(cmd.Environ(), func(kv string) bool {
			name, _, _ := strings.Cut(kv, "=")
			return slices.Contains(b.Hide, name)
		})
	}
	cmd.Stdout, cmd.Stderr = out, out
	// Its own process group, so a timeout or a cancel kills the whole pipeline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A background job that keeps the pipe open would otherwise hold Wait until it exits.
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	pgid := cmd.Process.Pid

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	var err error
	var stopped string
	select {
	case err = <-done:
	case <-timer.C:
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		err, stopped = <-done, fmt.Sprintf("killed after %d s timeout", timeout)
	case <-ctx.Done():
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
		return Result{}, ctx.Err()
	}

	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		return Result{}, err
	}

	text := out.String()
	var notes []string
	if stopped != "" {
		notes = append(notes, stopped)
	} else if syscall.Kill(-pgid, 0) == nil {
		b.Jobs.add(pgid)
		notes = append(notes, fmt.Sprintf("background processes still running in group %d; they stop when gilda exits", pgid))
	}
	if code != 0 {
		notes = append(notes, fmt.Sprintf("exit %d", code))
	}
	body := text
	if body == "" {
		body = "(no output)"
	}
	if len(notes) > 0 {
		body = strings.TrimRight(body, "\n") + "\n[" + strings.Join(notes, "; ") + "]"
	}
	return Result{
		Output:  body,
		Summary: bashSummary(text, code, stopped),
		Failed:  code != 0 || stopped != "",
	}, nil
}

func bashSummary(text string, code int, stopped string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	last := printable(lines[len(lines)-1])
	switch {
	case stopped != "":
		return stopped
	case code != 0 && last != "":
		return fmt.Sprintf("exit %d: %s", code, last)
	case code != 0:
		return fmt.Sprintf("exit %d", code)
	case text == "":
		return "no output"
	case len(lines) == 1:
		return last
	default:
		return plural(len(lines), "line")
	}
}

// printable keeps what a progress bar last drew on a line and drops control characters, which
// would move the cursor in the REPL.
func printable(s string) string {
	if i := strings.LastIndexByte(strings.TrimRight(s, "\r"), '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = ansiSeq.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
}

var ansiSeq = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|.)`)

// capture keeps the first fifth and the last four fifths of limit bytes, so a command that
// writes without stopping costs bounded memory.
type capture struct {
	limit   int
	head    []byte
	tail    []byte
	dropped int
}

func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.limit/5 - len(c.head); room > 0 {
		k := min(room, len(p))
		c.head = append(c.head, p[:k]...)
		p = p[k:]
	}
	c.tail = append(c.tail, p...)
	if keep := c.limit - c.limit/5; len(c.tail) > 2*keep {
		c.dropped += len(c.tail) - keep
		c.tail = append(c.tail[:0], c.tail[len(c.tail)-keep:]...)
	}
	return n, nil
}

func (c *capture) String() string {
	keep := c.limit - c.limit/5
	tail, dropped := c.tail, c.dropped
	if len(tail) > keep {
		dropped += len(tail) - keep
		tail = tail[len(tail)-keep:]
	}
	if dropped == 0 {
		return Cap(string(c.head)+string(tail), c.limit)
	}
	return Cap(fmt.Sprintf("%s\n[... %d bytes omitted ...]\n%s", c.head, dropped, tail), c.limit+64)
}

// Jobs records process groups left running by bash.
type Jobs struct {
	mu    sync.Mutex
	pgids []int
}

func (j *Jobs) add(pgid int) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.pgids = append(j.pgids, pgid)
	j.mu.Unlock()
}

// Kill stops every recorded process group.
func (j *Jobs) Kill() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, pgid := range j.pgids {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	j.pgids = nil
}
