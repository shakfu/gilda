package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/provider"
	"github.com/shakfu/gilda/state"
)

// sessionVersion is the format of a saved session. A newer one is refused rather than misread.
const sessionVersion = 1

// maxSessions bounds the saved sessions; the oldest go first.
const maxSessions = 100

// SessionInfo describes a saved session without its messages.
type SessionInfo struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Root     string    `json:"root"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	// Title is the first prompt, cut to one line.
	Title    string `json:"title"`
	Messages int    `json:"message_count"`
	// Used is the context the last request filled, so a resumed session elides or stops in
	// time.
	Used int64 `json:"context_used"`
}

type session struct {
	SessionInfo
	History []savedMessage `json:"messages"`
}

type savedMessage struct {
	llm.Message
	Native *savedNative `json:"native,omitempty"`
}

type savedNative struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Data     json.RawMessage `json:"data"`
}

// validID keeps a session id from naming a path outside the sessions directory.
var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func sessionDir(stateDir string) string { return filepath.Join(stateDir, "sessions") }

// Save writes the conversation to the state directory, replacing the previous save of the same
// session. It does nothing for an empty conversation or when settings.toml sets save = false.
// A payload whose adapter cannot encode it is saved without it, so that message resumes as
// text and tool calls.
//
// When another gilda saved the same session since this one last saved or loaded it, as after
// both resumed it, the conversation is saved under a new id instead, and the error says so.
func (a *App) Save() error {
	ag := a.Agent
	if !a.saveSessions || len(ag.History) == 0 || !validID.MatchString(ag.SessionID) {
		return nil
	}
	dir := sessionDir(a.State.Dir())
	var forked error
	if !a.saved.IsZero() {
		if info, err := readInfo(filepath.Join(dir, ag.SessionID+".json")); err == nil && !info.Updated.Equal(a.saved) {
			old := ag.SessionID
			ag.SessionID, a.created = newSessionID(), time.Time{}
			forked = fmt.Errorf("session %s was saved by another gilda; this conversation continues as %s", old, ag.SessionID)
		}
	}
	now := time.Now().UTC()
	first := a.created.IsZero()
	if first {
		a.created = now
	}
	s := session{SessionInfo: SessionInfo{
		Version: sessionVersion, ID: ag.SessionID, Root: a.opts.Root, Provider: a.ProviderID, Model: ag.Model,
		Created: a.created, Updated: now, Messages: len(ag.History), Used: ag.Used,
	}}
	for _, m := range ag.History {
		if s.Title == "" && m.Role == llm.User {
			s.Title = oneLine(m.Text, 80)
		}
		sm := savedMessage{Message: m}
		if n := m.Native; n != nil {
			if e, ok := provider.Find(n.Provider); ok && e.Codec != nil {
				if raw, err := e.Codec.Encode(n.Data); err == nil {
					sm.Native = &savedNative{Provider: n.Provider, Model: n.Model, Data: raw}
				}
			}
		}
		s.History = append(s.History, sm)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := state.WriteFile(filepath.Join(dir, s.ID+".json"), data); err != nil {
		return err
	}
	a.saved = now
	if first {
		prune(dir, maxSessions)
	}
	return forked
}

// Load replaces the conversation with the session saved for this root under id, or a unique
// prefix of it; "" means the newest. The provider and model stay; the session's reasoning
// replays only to the model that produced it. The warnings say what could not be restored.
func (a *App) Load(id string) (*SessionInfo, []error, error) {
	s, err := findSession(a.State.Dir(), a.opts.Root, id)
	if err != nil {
		return nil, nil, err
	}
	a.Agent.Reset()
	a.Seen.Reset()
	a.resume(s)
	warns := a.warns
	a.warns = nil
	return &s.SessionInfo, warns, nil
}

// SessionSaved reports whether the current conversation has been saved, so it can be resumed.
func (a *App) SessionSaved() bool { return a.saveSessions && !a.created.IsZero() }

// Sessions lists the sessions saved in stateDir for root, newest first. An empty stateDir means
// gilda's own; an empty root lists every session.
func Sessions(stateDir, root string) ([]SessionInfo, error) {
	if stateDir == "" {
		stateDir = state.StateDir()
	}
	entries, err := os.ReadDir(sessionDir(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := readInfo(filepath.Join(sessionDir(stateDir), e.Name()))
		if err != nil || root != "" && info.Root != root {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func readInfo(path string) (SessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer f.Close()
	var info SessionInfo
	err = json.NewDecoder(f).Decode(&info)
	return info, err
}

// findSession returns the session to resume: the newest for root when id is empty, else the one
// whose id is id or uniquely starts with it.
func findSession(stateDir, root, id string) (*session, error) {
	all, err := Sessions(stateDir, "")
	if err != nil {
		return nil, err
	}
	var match []SessionInfo
	for _, s := range all {
		if id == "" {
			if s.Root == root {
				match = append(match, s)
			}
			continue
		}
		if s.ID == id {
			match = []SessionInfo{s}
			break
		}
		if strings.HasPrefix(s.ID, id) || strings.HasPrefix(s.ID, "gilda-"+id) {
			match = append(match, s)
		}
	}
	switch {
	case len(match) == 0 && id == "":
		return nil, fmt.Errorf("no saved session for %s", root)
	case len(match) == 0:
		return nil, fmt.Errorf("no saved session %q; --sessions lists them", id)
	case id != "" && len(match) > 1:
		return nil, fmt.Errorf("%q matches %d sessions; give more of the id", id, len(match))
	}
	info := match[0]
	if !validID.MatchString(info.ID) {
		return nil, fmt.Errorf("saved session has an invalid id %q", info.ID)
	}
	if info.Root != root {
		return nil, fmt.Errorf("session %s belongs to %s; run gilda there, or pass -C %s", info.ID, info.Root, info.Root)
	}
	data, err := os.ReadFile(filepath.Join(sessionDir(stateDir), info.ID+".json"))
	if err != nil {
		return nil, err
	}
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session %s: %w", info.ID, err)
	}
	if s.Version > sessionVersion {
		return nil, fmt.Errorf("session %s was saved by a newer gilda (format %d)", s.ID, s.Version)
	}
	return &s, nil
}

// restore rebuilds the history. A payload that no adapter decodes is dropped, so its message
// replays as text and tool calls; dropped counts them.
func (s *session) restore() (history []llm.Message, dropped int) {
	for _, sm := range s.History {
		m := sm.Message
		if n := sm.Native; n != nil {
			if data, err := decodeNative(n); err == nil {
				m.Native = &llm.Native{Provider: n.Provider, Model: n.Model, Data: data}
			} else {
				dropped++
			}
		}
		history = append(history, m)
	}
	return history, dropped
}

func decodeNative(n *savedNative) (any, error) {
	e, ok := provider.Find(n.Provider)
	if !ok || e.Codec == nil {
		return nil, fmt.Errorf("no codec for %s", n.Provider)
	}
	return e.Codec.Decode(n.Data)
}

// prune removes the oldest session files beyond keep.
func prune(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if info, err := e.Info(); err == nil && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, file{e.Name(), info.ModTime()})
		}
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}

// oneLine joins s into one line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}
