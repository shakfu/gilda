package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateAndHistoryRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := Load("")
	s.Provider, s.Models["openai"] = "openai", "gpt-x"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if got := Load(""); got.Provider != "openai" || got.Models["openai"] != "gpt-x" {
		t.Fatalf("got %+v", got)
	}

	h := LoadHistory("")
	h.Add("one")
	h.Add("one")
	h.Add("two\nlines with a \\ backslash")
	h.Add("  ")
	got := LoadHistory("").Entries
	if len(got) != 2 || got[1] != "two\nlines with a \\ backslash" {
		t.Fatalf("got %q", got)
	}
}

func TestRelativeXDGIsIgnored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if d := ConfigDir(); d == "relative/gilda" {
		t.Fatal("used a relative XDG path")
	}
}

func TestAnExplicitDirectoryIsUsed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	s := Load(dir)
	s.Provider = "anthropic"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if Load(dir).Provider != "anthropic" || Load("").Provider != "" {
		t.Fatal("state was not kept in the given directory alone")
	}
}

func TestSettings(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadSettings(dir); err != nil || len(s.Permissions.Secrets) != 0 {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	write := func(text string) {
		if err := os.WriteFile(filepath.Join(dir, SettingsFile), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[permissions]\nmode = \"ask\"\nsecrets = [\"*.pem\"]\nprotected = [\"go.sum\"]\ncommands = [\"go test\"]\ndiff = false\n")
	s, err := LoadSettings(dir)
	if err != nil || s.Permissions.Mode != "ask" || s.Permissions.Secrets[0] != "*.pem" ||
		s.Permissions.Protected[0] != "go.sum" || s.Permissions.Commands[0] != "go test" || s.Permissions.Diff == nil || *s.Permissions.Diff {
		t.Fatalf("%+v %v", s, err)
	}
	for text, want := range map[string]string{
		"[permissions]\nsecret = [\"x\"]\n":            "unknown keys: permissions.secret",
		"[permissions]\nsecrets = [\"[\"]\n":           "bad path pattern",
		"[permissions\n":                               SettingsFile,
		"[permissions]\nmode = \"yolo\"\n":             "permissions must be",
		"[permissions]\ncommands = [\"a | b\"]\n":      "bad command",
		"[permissions]\nhosts = [\"https://x.com\"]\n": "bad host",
		"[agent]\nmax_tokens = 0\n":                    "agent.max_tokens must be at least 1",
		"[repl]\nnotify = \"loud\"\n":                  "repl.notify must be bell, osc9 or off",
		"[agent]\nstream_retries = -1\n":               "agent.stream_retries must be at least 0",
		"[tools]\noutput_cap = 100\n":                  "tools.output_cap must be at least 4096",
		"[prompt]\nskill = false\n":                    "unknown keys: prompt.skill",
	} {
		write(text)
		if _, err := LoadSettings(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", text, err, want)
		}
	}
}
