package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/llm/anthropic"
)

// sessionApp builds a mock app whose state and config live under dir.
func sessionApp(t *testing.T, dir, root string, opts Options) *App {
	t.Helper()
	script := filepath.Join(dir, "m.json")
	if err := os.WriteFile(script, []byte(`[{"text":"one"},{"text":"two"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Mock, opts.Root, opts.StateDir, opts.ConfigDir, opts.CacheDir = script, root,
		filepath.Join(dir, "state"), filepath.Join(dir, "cfg"), filepath.Join(dir, "cache")
	a, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestASavedSessionResumes(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a := sessionApp(t, dir, root, Options{})
	if _, err := a.Agent.Run(context.Background(), "first prompt", nil); err != nil {
		t.Fatal(err)
	}
	// An anthropic payload, as the adapter keeps it, must come back as one.
	native, err := anthropic.Codec.Decode(json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"one"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	a.Agent.History[1].Native = &llm.Native{Provider: "anthropic", Model: "claude-x", Data: native}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	list, err := Sessions(filepath.Join(dir, "state"), root)
	if err != nil || len(list) != 1 || list[0].Title != "first prompt" || list[0].Messages != 2 {
		t.Fatalf("sessions %+v %v", list, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "state", "sessions", list[0].ID+".json")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}

	b := sessionApp(t, dir, root, Options{Continue: true})
	h := b.Agent.History
	if len(h) != 2 || h[0].Text != "first prompt" || h[1].Text != "one" || b.Agent.SessionID != list[0].ID {
		t.Fatalf("history %+v id %s", h, b.Agent.SessionID)
	}
	if got, ok := h[1].NativeFor("anthropic", "claude-x"); !ok {
		t.Fatal("native payload was lost")
	} else if raw, err := anthropic.Codec.Encode(got); err != nil || !strings.Contains(string(raw), `"one"`) {
		t.Fatalf("native %s %v", raw, err)
	}
	if _, err := b.Agent.Run(context.Background(), "second", nil); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	// Still one session: a resumed one is saved under its own id.
	if list, _ := Sessions(filepath.Join(dir, "state"), root); len(list) != 1 || list[0].Messages != 4 {
		t.Fatalf("sessions %+v", list)
	}

	c := sessionApp(t, dir, root, Options{Resume: strings.TrimPrefix(list[0].ID, "gilda-")[:6]})
	if len(c.Agent.History) != 4 {
		t.Fatalf("resume by prefix: %d messages", len(c.Agent.History))
	}
}

func TestAPayloadNoAdapterReadsResumesAsText(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a := sessionApp(t, dir, root, Options{})
	if _, err := a.Agent.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state", "sessions", a.Agent.SessionID+".json")
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), `"role":"assistant"`, `"role":"assistant","native":{"provider":"anthropic","model":"m","data":"not a message"}`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b := sessionApp(t, dir, root, Options{Continue: true})
	if b.Agent.History[1].Native != nil {
		t.Fatal("an unreadable payload was kept")
	}
	warns, err := b.Prepare(context.Background())
	if err != nil || len(warns) != 1 || !strings.Contains(warns[0].Error(), "resume as text") {
		t.Fatalf("warns %v %v", warns, err)
	}
}

func TestResumeRefusals(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	opts := func(o Options) error {
		script := filepath.Join(dir, "m.json")
		_ = os.WriteFile(script, []byte(`[{"text":"one"}]`), 0o600)
		o.Mock, o.StateDir, o.ConfigDir = script, filepath.Join(dir, "state"), filepath.Join(dir, "cfg")
		_, err := New(o)
		return err
	}
	if err := opts(Options{Root: root, Continue: true}); err == nil || !strings.Contains(err.Error(), "no saved session") {
		t.Fatalf("nothing saved: %v", err)
	}
	a := sessionApp(t, dir, root, Options{})
	if _, err := a.Agent.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	// History names files under root; resuming elsewhere would point the model at the wrong tree.
	if err := opts(Options{Root: t.TempDir(), Resume: a.Agent.SessionID}); err == nil || !strings.Contains(err.Error(), "belongs to") {
		t.Fatalf("other root: %v", err)
	}
	if err := opts(Options{Root: root, Resume: "../../etc"}); err == nil {
		t.Fatal("a path was accepted as an id")
	}
	if err := opts(Options{Root: root, Continue: true, Resume: "x"}); err == nil {
		t.Fatal("--continue with --resume was accepted")
	}
}

func TestSavingCanBeTurnedOff(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cfg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cfg", "settings.toml"), []byte("[session]\nsave = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := sessionApp(t, dir, root, Options{})
	if _, err := a.Agent.Run(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if list, _ := Sessions(filepath.Join(dir, "state"), ""); len(list) != 0 {
		t.Fatalf("saved %+v", list)
	}
}

func TestResetStartsANewSession(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a := sessionApp(t, dir, root, Options{})
	for _, p := range []string{"one", "two"} {
		if _, err := a.Agent.Run(context.Background(), p, nil); err != nil {
			t.Fatal(err)
		}
		if err := a.Save(); err != nil {
			t.Fatal(err)
		}
		a.Reset()
	}
	if list, _ := Sessions(filepath.Join(dir, "state"), root); len(list) != 2 || list[0].Title != "two" {
		t.Fatalf("sessions %+v", list)
	}
}

func TestPruneKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+".json")
		_ = os.WriteFile(p, []byte("{}"), 0o600)
		ts := mustTime(t, i)
		_ = os.Chtimes(p, ts, ts)
	}
	prune(dir, 3)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "c.json,d.json,e.json" {
		t.Fatalf("kept %v", names)
	}
}

func mustTime(t *testing.T, i int) time.Time {
	t.Helper()
	return time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC)
}

// Two instances resuming one session keep both conversations: the second to save forks.
func TestConcurrentResumesFork(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	a := sessionApp(t, dir, root, Options{})
	if _, err := a.Agent.Run(context.Background(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	x := sessionApp(t, dir, root, Options{Continue: true})
	y := sessionApp(t, dir, root, Options{Continue: true})
	for _, b := range []*App{x, y} {
		if _, err := b.Agent.Run(context.Background(), "more", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.Save(); err != nil {
		t.Fatal(err)
	}
	err := y.Save()
	if err == nil || !strings.Contains(err.Error(), "saved by another gilda") || y.Agent.SessionID == x.Agent.SessionID {
		t.Fatalf("second save: %v, ids %s %s", err, x.Agent.SessionID, y.Agent.SessionID)
	}
	if list, _ := Sessions(filepath.Join(dir, "state"), root); len(list) != 2 {
		t.Fatalf("sessions %+v", list)
	}
	// Each keeps saving to its own file from then on.
	if err := x.Save(); err != nil {
		t.Fatal(err)
	}
	if err := y.Save(); err != nil {
		t.Fatal(err)
	}
}
