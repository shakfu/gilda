package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/shakfu/gilda/permission"
)

// fakeHerdr records the commands a reporter runs.
type fakeHerdr struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fakeHerdr) reporter() *herdr {
	h := &herdr{bin: "/herdr", pane: "w1:p1", resumable: true, run: func(_ context.Context, bin string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, args)
		return nil
	}}
	h.cond = sync.NewCond(&h.mu)
	return h
}

// last waits for h to send what it holds and returns the last command run.
func (f *fakeHerdr) last(h *herdr) []string {
	h.mu.Lock()
	h.wait()
	h.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

// flag returns the value after name in args.
func flag(args []string, name string) string {
	if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// resumeOf returns the arguments after "--".
func resumeOf(args []string) []string {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[i+1:]
	}
	return nil
}

func TestHerdrNeedsItsEnvironment(t *testing.T) {
	env := map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1", "HERDR_BIN_PATH": "/herdr", "HERDR_SOCKET_PATH": "/s"}
	if newHerdr(func(k string) string { return env[k] }) == nil {
		t.Fatal("no reporter with every variable set")
	}
	for k := range env {
		get := func(n string) string {
			if n == k {
				return ""
			}
			return env[n]
		}
		if newHerdr(get) != nil {
			t.Errorf("a reporter without %s", k)
		}
	}
}

func TestHerdrFollowsARun(t *testing.T) {
	m := testModel(t)
	defer m.tasks.wait()
	f := &fakeHerdr{}
	m.herdr = f.reporter()

	m.start("hi")
	if got := f.last(m.herdr); flag(got, "--state") != "working" || resumeOf(got) != nil {
		t.Fatalf("at start: %q", got)
	}
	drain(t, m)
	got := f.last(m.herdr)
	id := m.app.Agent.SessionID
	want := []string{"gilda", "--resume", id, "-C", m.app.Root()}
	if flag(got, "--state") != "idle" || flag(got, "--agent-session-id") != id || !slices.Equal(resumeOf(got), want) {
		t.Fatalf("after the run: %q", got)
	}
	if got[2] != "w1:p1" {
		t.Errorf("pane not first: %q", got)
	}

	ask(m, "edit", permission.Reason{Kind: permission.Outside, Text: "edit outside"})
	if got := f.last(m.herdr); flag(got, "--state") != "blocked" || !strings.Contains(flag(got, "--message"), "edit x") {
		t.Fatalf("at an approval: %q", got)
	}
	press(m, "n")
	if got := f.last(m.herdr); flag(got, "--state") != "working" {
		t.Fatalf("after the answer: %q", got)
	}

	m.herdr.release()
	if got := f.last(m.herdr); got[1] != "release-agent" || got[2] != "w1:p1" {
		t.Fatalf("release: %q", got)
	}
}

func TestHerdrSendsOnlyTheLatestReport(t *testing.T) {
	f := &fakeHerdr{}
	h := f.reporter()
	run := h.run
	started, unblock := make(chan struct{}), make(chan struct{})
	h.run = func(ctx context.Context, bin string, args ...string) error {
		if flag(args, "--state") == "working" {
			close(started)
			<-unblock
		}
		return run(ctx, bin, args...)
	}
	h.report("working", "", "", nil)
	<-started
	h.report("blocked", "", "", nil)
	h.report("idle", "", "", nil)
	close(unblock)
	f.last(h)
	h.report("blocked", "", "", nil)
	h.release()

	var states []string
	var prev int64
	for _, c := range f.calls {
		states = append(states, flag(c, "--state"))
		seq, err := strconv.ParseInt(flag(c, "--seq"), 10, 64)
		if err != nil || seq <= prev {
			t.Errorf("seq %q after %d", flag(c, "--seq"), prev)
		}
		prev = seq
	}
	// The second blocked may or may not be sent before the release; the first never is.
	if !slices.Equal(states, []string{"working", "idle", "blocked", ""}) && !slices.Equal(states, []string{"working", "idle", ""}) {
		t.Errorf("states %q", states)
	}
}

func TestHerdrResumeCommand(t *testing.T) {
	h := (&fakeHerdr{}).reporter()
	if got := h.resumeArgv("/r", permission.Ask, "s1"); !slices.Equal(got, []string{"gilda", "--resume", "s1", "-C", "/r", "--permissions", "ask"}) {
		t.Errorf("ask mode: %q", got)
	}
	for _, root := range []string{"/it's", "/a\nb", "/" + strings.Repeat("x", 8<<10)} {
		if got := h.resumeArgv(root, permission.Auto, "s1"); got != nil {
			t.Errorf("root %.20q: %q", root, got)
		}
	}
	h.resumable = false
	if got := h.resumeArgv("/r", permission.Auto, "s1"); got != nil {
		t.Errorf("gilda not on PATH: %q", got)
	}
}
