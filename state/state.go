// Package state keeps what gilda remembers between runs: the last provider, the last model per
// provider, and REPL history. Directories are 0700 and files 0600, because prompts are stored
// verbatim.
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const app = "gilda"

func xdg(env, fallback string) string {
	if d := os.Getenv(env); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, app)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, fallback, app)
}

// ConfigDir holds the user's AGENTS.md and skills/.
func ConfigDir() string { return xdg("XDG_CONFIG_HOME", ".config") }

// StateDir holds state.json and history.
func StateDir() string { return xdg("XDG_STATE_HOME", ".local/state") }

// CacheDir holds the price list.
func CacheDir() string { return xdg("XDG_CACHE_HOME", ".cache") }

type State struct {
	Provider string            `json:"provider,omitempty"`
	Models   map[string]string `json:"models,omitempty"`
	Effort   string            `json:"effort,omitempty"`
	// Trust records, by directory, whether the user let its AGENTS.md files instruct an agent
	// that runs bash without asking.
	Trust map[string]bool `json:"trust,omitempty"`
	dir   string
}

// Load returns the state saved in dir, or an empty one. An empty dir means StateDir.
func Load(dir string) State {
	if dir == "" {
		dir = StateDir()
	}
	s := State{dir: dir}
	if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Models == nil {
		s.Models = map[string]string{}
	}
	return s
}

// Save writes the state through a temporary file and a rename.
func (s State) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(filepath.Join(s.dir, "state.json"), data)
}

// Dir is where the state is saved; the REPL keeps its history there too.
func (s State) Dir() string { return s.dir }

// WriteFile replaces p through a temporary file of its own, so two gilda instances saving at
// once never rename each other's half-written file into place. The file is 0600.
func WriteFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

const historyMax = 1000

// History is the REPL's prompt history, oldest first. Multi-line entries are stored with
// escaped newlines, one per line.
type History struct {
	Entries []string
	file    string
}

// LoadHistory reads the history saved in dir. An empty dir means StateDir.
func LoadHistory(dir string) *History {
	if dir == "" {
		dir = StateDir()
	}
	h := &History{file: filepath.Join(dir, "history")}
	f, err := os.Open(h.file)
	if err != nil {
		return h
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			h.Entries = append(h.Entries, unescape(line))
		}
	}
	return h
}

// Add appends an entry, skipping an immediate repeat, and saves the file.
func (h *History) Add(entry string) {
	if strings.TrimSpace(entry) == "" {
		return
	}
	if n := len(h.Entries); n > 0 && h.Entries[n-1] == entry {
		return
	}
	h.Entries = append(h.Entries, entry)
	if len(h.Entries) > historyMax {
		h.Entries = h.Entries[len(h.Entries)-historyMax:]
	}
	var b strings.Builder
	for _, e := range h.Entries {
		b.WriteString(escape(e))
		b.WriteByte('\n')
	}
	_ = WriteFile(h.file, []byte(b.String()))
}

var escaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			if s[i] == 'n' {
				b.WriteByte('\n')
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
