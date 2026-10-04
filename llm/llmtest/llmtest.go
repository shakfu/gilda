// Package llmtest serves scripted server-sent events, so adapter tests run each SDK against a
// local endpoint and inspect the request it sent.
package llmtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/shakfu/gilda/llm"
)

// Server replies to each POST with the next script, in order, and records the request bodies.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	scripts []string
	Bodies  []map[string]any
	Paths   []string
}

// New starts a server. Each script is raw SSE text; lines are sent as written.
func New(t *testing.T, scripts ...string) *Server {
	t.Helper()
	s := &Server{scripts: scripts}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	s.mu.Lock()
	s.Bodies = append(s.Bodies, body)
	s.Paths = append(s.Paths, r.URL.Path)
	if len(s.scripts) == 0 {
		s.mu.Unlock()
		http.Error(w, `{"error":{"message":"no script"}}`, http.StatusInternalServerError)
		return
	}
	script := s.scripts[0]
	s.scripts = s.scripts[1:]
	s.mu.Unlock()

	if code, body, ok := strings.Cut(script, "\n"); ok && strings.HasPrefix(code, "status ") {
		var status int
		fmt.Sscanf(code, "status %d", &status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, script)
}

// Status is a script that answers with an HTTP error instead of a stream.
func Status(code int, body string) string { return fmt.Sprintf("status %d\n%s", code, body) }

// SSE joins events given as (event name, data) pairs; an empty name omits the event line.
func SSE(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] != "" {
			b.WriteString("event: " + pairs[i] + "\n")
		}
		b.WriteString("data: " + pairs[i+1] + "\n\n")
	}
	return b.String()
}

// Get walks a decoded JSON body by keys and indexes, returning nil when a step is missing.
func Get(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

// Restore passes m's Native payload through codec, as saving and resuming a session does.
func Restore(t *testing.T, codec llm.NativeCodec, m llm.Message) llm.Message {
	t.Helper()
	raw, err := codec.Encode(m.Native.Data)
	if err != nil {
		t.Fatal(err)
	}
	data, err := codec.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := *m.Native
	n.Data = data
	m.Native = &n
	return m
}

// ImageHistory is a prompt, a call to read and a result that carries one PNG image.
func ImageHistory(callID string) []llm.Message {
	return []llm.Message{
		{Role: llm.User, Text: "look"},
		{Role: llm.Assistant, Calls: []llm.ToolCall{{ID: callID, Name: "read", Arguments: `{"path":"a.png"}`}}},
		{Role: llm.Tool, Results: []llm.ToolResult{{CallID: callID, Content: "a.png is a PNG image",
			Images: []llm.Image{{MediaType: "image/png", Data: []byte("PNGDATA")}}}}},
	}
}

// PNGDataURL is the data URL of ImageHistory's image.
const PNGDataURL = "data:image/png;base64,UE5HREFUQQ=="
