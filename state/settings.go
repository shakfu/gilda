package state

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/shakfu/gilda/permission"
)

// SettingsFile is the name of the user's settings in the config directory.
const SettingsFile = "settings.toml"

// Settings is what the user sets in settings.toml:
//
//	[permissions]
//	mode = "ask"
//	secrets = ["*.pem", "secrets/*"]
//	protected = ["go.sum", "migrations"]
//	diff = false
//
//	[agent]
//	max_tokens = 16000
//	stream_retries = 0
//
//	[tools]
//	output_cap = 16384
//
//	[prompt]
//	skills = false
//
//	[prices]
//	fetch = false
//
//	[session]
//	save = false
//
// The mode applies when neither --permissions nor GILDA_PERMISSIONS sets one; max_tokens,
// max_turns and context apply when their flags are not given. The patterns add to the built-in
// ones and cannot lift them. A key left out takes gilda's default.
type Settings struct {
	Permissions Permissions `toml:"permissions"`
	Agent       Agent       `toml:"agent"`
	Tools       Tools       `toml:"tools"`
	Prompt      Prompt      `toml:"prompt"`
	Prices      Prices      `toml:"prices"`
	Session     Session     `toml:"session"`
	REPL        REPL        `toml:"repl"`
}

// REPL sets how the REPL gets the user's attention when a long turn ends or needs an approval:
// "bell", "osc9" (a desktop notification in terminals that support it) or "off". Empty means
// bell.
type REPL struct {
	Notify string `toml:"notify"`
}

// Session says whether conversations are saved for --continue and --resume. Nil means on.
type Session struct {
	Save *bool `toml:"save"`
}

// Agent bounds a prompt's round-trips. See agent.Config.
type Agent struct {
	MaxTokens     *int64 `toml:"max_tokens"`
	MaxTurns      *int   `toml:"max_turns"`
	Context       *int64 `toml:"context"`
	StreamRetries *int   `toml:"stream_retries"`
}

// Tools bounds the built-in tools. See tool.Limits.
type Tools struct {
	OutputCap      *int `toml:"output_cap"`
	ReadLines      *int `toml:"read_lines"`
	ReadLineBytes  *int `toml:"read_line_bytes"`
	BashTimeout    *int `toml:"bash_timeout"`
	BashMaxTimeout *int `toml:"bash_max_timeout"`
	// BashEnv names provider key variables that bash still receives; see app.HiddenEnv.
	BashEnv []string `toml:"bash_env"`
	// ApplyPatch offers apply_patch alongside edit; nil offers it to OpenAI models only.
	ApplyPatch *bool `toml:"apply_patch"`
	// Task offers the task tool, which runs a subagent; nil means on.
	Task *bool `toml:"task"`
}

// Prompt says what goes into the system prompt besides gilda's own text. Nil means on.
type Prompt struct {
	AgentsMD *bool `toml:"agents_md"`
	Skills   *bool `toml:"skills"`
}

// Prices controls OpenRouter's price list, which also gives context windows. Nil means on.
type Prices struct {
	Fetch *bool `toml:"fetch"`
}

type Permissions struct {
	// Mode is auto, ask, all or read-only; empty leaves the default, auto.
	Mode             permission.Mode `toml:"mode"`
	permission.Rules                 // secrets and protected
	// Diff shows what a call would change, where the tool can say, when asking to approve it.
	// Nil means on. DiffMaxBytes bounds the existing file a write preview reads.
	Diff         *bool `toml:"diff"`
	DiffMaxBytes *int  `toml:"diff_max_bytes"`
}

// LoadSettings reads settings.toml from dir; an empty dir means ConfigDir. A missing file is
// no settings. An unknown key is an error, so a misspelled one is not silently ignored.
func LoadSettings(dir string) (Settings, error) {
	if dir == "" {
		dir = ConfigDir()
	}
	path := filepath.Join(dir, SettingsFile)
	var s Settings
	meta, err := toml.DecodeFile(path, &s)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Settings{}, nil
	case err != nil:
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	if extra := meta.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return Settings{}, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if s.Permissions.Mode != "" {
		if _, err := permission.Parse(string(s.Permissions.Mode)); err != nil {
			return Settings{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := s.Permissions.Validate(); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// validate checks the numbers: a zero would read as "use the default" further in, so each must
// be set to a real value. stream_retries alone may be 0.
func (s Settings) validate() error {
	checks := []struct {
		key string
		v   *int64
		min int64
	}{
		{"agent.max_tokens", s.Agent.MaxTokens, 1},
		{"agent.max_turns", widen(s.Agent.MaxTurns), 1},
		{"agent.context", s.Agent.Context, 1},
		{"agent.stream_retries", widen(s.Agent.StreamRetries), 0},
		// bash keeps 1 KiB of the cap for its own notes.
		{"tools.output_cap", widen(s.Tools.OutputCap), 4096},
		{"tools.read_lines", widen(s.Tools.ReadLines), 1},
		{"tools.read_line_bytes", widen(s.Tools.ReadLineBytes), 1},
		{"tools.bash_timeout", widen(s.Tools.BashTimeout), 1},
		{"tools.bash_max_timeout", widen(s.Tools.BashMaxTimeout), 1},
		{"permissions.diff_max_bytes", widen(s.Permissions.DiffMaxBytes), 1},
	}
	for _, c := range checks {
		if c.v != nil && *c.v < c.min {
			return fmt.Errorf("%s must be at least %d", c.key, c.min)
		}
	}
	switch s.REPL.Notify {
	case "", "bell", "osc9", "off":
	default:
		return fmt.Errorf("repl.notify must be bell, osc9 or off, not %q", s.REPL.Notify)
	}
	for _, name := range s.Tools.BashEnv {
		if name == "" || strings.ContainsAny(name, "= ") {
			return fmt.Errorf("tools.bash_env: bad variable name %q", name)
		}
	}
	return nil
}

func widen(v *int) *int64 {
	if v == nil {
		return nil
	}
	w := int64(*v)
	return &w
}

// Or returns *v, or def when v is nil.
func Or[T any](v *T, def T) T {
	if v == nil {
		return def
	}
	return *v
}
