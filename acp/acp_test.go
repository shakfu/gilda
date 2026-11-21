package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shakfu/gilda/app"
)

// msg is any JSON-RPC message the client sees.
type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// update is a session/update notification's params.
type update struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       any    `json:"content"`
		ToolCallID    string `json:"toolCallId"`
		Title         string `json:"title"`
		Kind          string `json:"kind"`
		Status        string `json:"status"`
		Cost          *struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"cost"`
	} `json:"update"`
}

// client drives Serve over pipes. permission answers each session/request_permission with an
// outcome; nil cancels it.
type client struct {
	t          *testing.T
	in         io.WriteCloser
	msgs       chan msg
	done       chan error
	nextID     int
	mu         sync.Mutex
	asked      []json.RawMessage
	permission func(params json.RawMessage) any
}

// serve starts Serve with a mock script and state in temporary directories. It returns the
// client and the session cwd.
func serve(t *testing.T, script string, mutate ...func(*app.Options)) (*client, string) {
	t.Helper()
	dir := t.TempDir()
	state := t.TempDir()
	mock := filepath.Join(state, "mock.json")
	if err := os.WriteFile(mock, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}
	for _, f := range mutate {
		f(&o)
	}
	return serveWith(t, o), dir
}

func serveWith(t *testing.T, o app.Options) *client {
	t.Helper()
	return serveOpts(t, Options{App: o, Version: "test"})
}

func serveOpts(t *testing.T, o Options) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, in: inW, msgs: make(chan msg, 1024), done: make(chan error, 1)}
	go func() {
		c.done <- Serve(context.Background(), inR, outW, o)
		outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(nil, 1<<24)
		for sc.Scan() {
			var m msg
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("stdout carried a line that is not JSON: %q", sc.Text())
				continue
			}
			if m.Method == "session/request_permission" && m.ID != nil {
				c.mu.Lock()
				c.asked = append(c.asked, m.Params)
				answer := c.permission
				c.mu.Unlock()
				result := any(map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
				if answer != nil {
					result = answer(m.Params)
				}
				c.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
				continue
			}
			c.msgs <- m
		}
		close(c.msgs)
	}()
	t.Cleanup(func() {
		c.in.Close()
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return within 5 s of its input closing")
		}
	})
	return c
}

func (c *client) write(v any) {
	data, _ := json.Marshal(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Errorf("write: %v", err)
	}
}

// send sends a request and returns its id, for wait.
func (c *client) send(method string, params any) int {
	id, data := c.request(method, params)
	c.writeLine(data)
	return id
}

// request numbers a request and encodes it as one line.
func (c *client) request(method string, params any) (int, []byte) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id, append(data, '\n')
}

// writeLine writes raw lines in one write.
func (c *client) writeLine(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.in.Write(data); err != nil {
		c.t.Errorf("write: %v", err)
	}
}

func cancelLine(session string) []byte {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": session}})
	return append(data, '\n')
}

func textPrompt(session, text string) map[string]any {
	return map[string]any{"sessionId": session, "prompt": []map[string]string{{"type": "text", "text": text}}}
}

// wait returns the response to id and the updates that came before it.
func (c *client) wait(id int) (msg, []update) {
	c.t.Helper()
	var updates []update
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-c.msgs:
			if !ok {
				c.t.Fatalf("output ended before the response to %d", id)
			}
			if m.Method == "session/update" {
				var u update
				if err := json.Unmarshal(m.Params, &u); err != nil {
					c.t.Fatalf("update: %v", err)
				}
				updates = append(updates, u)
				continue
			}
			if string(m.ID) == fmt.Sprint(id) {
				return m, updates
			}
		case <-timeout:
			c.t.Fatalf("no response to %d within 10 s", id)
		}
	}
}

func (c *client) call(method string, params any) (msg, []update) {
	c.t.Helper()
	return c.wait(c.send(method, params))
}

func (c *client) init() {
	c.t.Helper()
	if m, _ := c.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}); m.Error != nil {
		c.t.Fatalf("initialize: %v", m.Error)
	}
}

func (c *client) newSession(cwd string) string {
	c.t.Helper()
	m, _ := c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if m.Error != nil || json.Unmarshal(m.Result, &r) != nil || r.SessionID == "" {
		c.t.Fatalf("session/new: %v %s", m.Error, m.Result)
	}
	return r.SessionID
}

func (c *client) prompt(session, text string) (msg, []update) {
	c.t.Helper()
	return c.call("session/prompt", map[string]any{"sessionId": session, "prompt": []map[string]string{{"type": "text", "text": text}}})
}

func stopReason(t *testing.T, m msg) string {
	t.Helper()
	if m.Error != nil {
		t.Fatalf("prompt failed: %v", m.Error)
	}
	var r struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(m.Result, &r)
	return r.StopReason
}

// kinds lists the updates as sessionUpdate[:status].
func kinds(updates []update) string {
	var out []string
	for _, u := range updates {
		k := u.Update.SessionUpdate
		if u.Update.Status != "" {
			k += ":" + u.Update.Status
		}
		out = append(out, k)
	}
	return strings.Join(out, " ")
}

func TestInitializeAndErrors(t *testing.T) {
	c, dir := serve(t, `[]`)
	if m, _ := c.call("session/new", map[string]any{"cwd": dir}); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("session/new before initialize: %+v", m)
	}
	m, _ := c.call("initialize", map[string]any{"protocolVersion": 1})
	var r struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
		AgentInfo struct{ Name, Version string } `json:"agentInfo"`
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || r.ProtocolVersion != 1 || !r.AgentCapabilities.LoadSession || r.AgentInfo.Name != "gilda" {
		t.Fatalf("initialize: %s", m.Result)
	}
	if m, _ := c.call("no/such", nil); m.Error == nil || m.Error.Code != codeMethodNotFound {
		t.Errorf("unknown method: %+v", m)
	}
	if m, _ := c.call("session/new", map[string]any{"cwd": "relative"}); m.Error == nil || m.Error.Code != codeInvalidParams {
		t.Errorf("relative cwd: %+v", m)
	}
	if m, _ := c.prompt("nope", "hi"); m.Error == nil || m.Error.Code != codeNotFound {
		t.Errorf("unknown session: %+v", m)
	}
	c.mu.Lock()
	c.in.Write([]byte("{not json\n"))
	c.mu.Unlock()
	m = <-c.msgs
	if m.Error == nil || m.Error.Code != codeParse || string(m.ID) != "null" {
		t.Errorf("parse error: %+v", m)
	}
}

// The mock turn of the usage tests: a tool call, then an answer, each with its cost.
const costedTurn = `[
  {"text": "Listing.", "calls": [{"name": "bash", "arguments": {"command": "echo hi"}}], "usage": {"input_tokens": 10, "output_tokens": 2, "cost": 0.001}},
  {"text": "The answer.", "usage": {"input_tokens": 20, "output_tokens": 3, "cost": 0.002}}
]`

// A turn streams its text, each tool call and the session's cost before the response.
func TestPromptStreamsUpdatesBeforeTheResponse(t *testing.T) {
	c, dir := serve(t, costedTurn, func(o *app.Options) { o.Context = 200000 })
	c.init()
	session := c.newSession(dir)
	m, updates := c.prompt(session, "go")
	if got := stopReason(t, m); got != "end_turn" {
		t.Fatalf("stopReason %q", got)
	}
	want := "agent_message_chunk usage_update tool_call:pending tool_call_update:in_progress tool_call_update:completed " +
		"agent_message_chunk agent_message_chunk usage_update"
	if got := kinds(updates); got != want {
		t.Fatalf("updates\n got %s\nwant %s", got, want)
	}
	call := updates[2].Update
	if call.Kind != "execute" || call.Title != "$ echo hi" || call.ToolCallID == "" {
		t.Errorf("tool_call %+v", call)
	}
	last := updates[len(updates)-1]
	if last.SessionID != session || last.Update.Cost == nil || last.Update.Cost.Currency != "USD" ||
		fmt.Sprintf("%.4f", last.Update.Cost.Amount) != "0.0030" {
		t.Errorf("usage_update is not the session's cumulative cost: %+v", last.Update.Cost)
	}
}

// A call that auto mode asks about is asked of the client. Anything but an allow option denies
// it; allow_always covers later calls of the same kind.
func TestPermissionRequests(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "out.txt")
	write := fmt.Sprintf(`{"name": "write", "arguments": {"path": %q, "content": "x"}}`, outside)
	step := `{"text": "", "calls": [` + write + `]}`
	script := `[` + step + `, {"text": "a"}, ` + step + `, {"text": "b"}, ` + step + `, {"text": "c"}, ` + step + `, {"text": "d"}]`
	c, dir := serve(t, script)
	c.init()
	session := c.newSession(dir)

	answers := []any{
		map[string]any{"outcome": map[string]string{"outcome": "cancelled"}},
		map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": "no-such-option"}},
		map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": "allow_always"}},
	}
	for i, answer := range answers {
		c.mu.Lock()
		c.permission = func(json.RawMessage) any { return answer }
		c.mu.Unlock()
		m, updates := c.prompt(session, "write it")
		stopReason(t, m)
		got := kinds(updates)
		allowed := i == 2
		if allowed != strings.Contains(got, "tool_call_update:completed") || allowed == strings.Contains(got, "tool_call_update:failed") {
			t.Errorf("answer %d: updates %s", i, got)
		}
		if _, err := os.Stat(outside); allowed != (err == nil) {
			t.Errorf("answer %d: file written %v", i, err == nil)
		}
	}
	// The fourth call is covered by allow_always, so the client is not asked again.
	os.Remove(outside)
	m, _ := c.prompt(session, "write it")
	stopReason(t, m)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.asked) != 3 {
		t.Fatalf("asked %d times, want 3", len(c.asked))
	}
	var req struct {
		ToolCall struct {
			ToolCallID, Kind string
		} `json:"toolCall"`
		Options []struct{ OptionID, Kind string } `json:"options"`
	}
	if err := json.Unmarshal(c.asked[0], &req); err != nil || req.ToolCall.Kind != "edit" || req.ToolCall.ToolCallID == "" {
		t.Fatalf("request %s", c.asked[0])
	}
	var opts []string
	for _, o := range req.Options {
		opts = append(opts, o.Kind)
	}
	if strings.Join(opts, " ") != "allow_once allow_always reject_once" {
		t.Errorf("options %v", opts)
	}
}

// session/cancel stops a running tool and the prompt answers cancelled, not an error.
func TestCancelEndsThePromptCancelled(t *testing.T) {
	c, dir := serve(t, `[{"calls": [{"name": "bash", "arguments": {"command": "sleep 30"}}]}, {"text": "never"}]`)
	c.init()
	session := c.newSession(dir)
	id := c.send("session/prompt", map[string]any{"sessionId": session, "prompt": []map[string]string{{"type": "text", "text": "wait"}}})
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	c.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": session}})
	m, updates := c.wait(id)
	if got := stopReason(t, m); got != "cancelled" {
		t.Fatalf("stopReason %q", got)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("cancel took %v", time.Since(start))
	}
	if !strings.Contains(kinds(updates), "tool_call_update:failed") {
		t.Errorf("updates %s", kinds(updates))
	}
}

func TestStopReasonsAndErrors(t *testing.T) {
	cases := []struct {
		script, want string
		turns        int
	}{
		{`[{"text": "cut", "stop": "max_tokens"}]`, "max_tokens", 0},
		{`[{"text": "no", "stop": "refusal"}]`, "refusal", 0},
		{`[{"calls": [{"name": "bash", "arguments": {"command": "true"}}]}]`, "max_turn_requests", 1},
		{`[{"error": "upstream exploded"}]`, "error", 0},
	}
	for _, tc := range cases {
		c, dir := serve(t, tc.script, func(o *app.Options) { o.MaxTurns = tc.turns })
		c.init()
		m, _ := c.prompt(c.newSession(dir), "go")
		if tc.want == "error" {
			if m.Error == nil || !strings.Contains(m.Error.Message, "upstream exploded") {
				t.Errorf("a provider error answered %+v", m)
			}
			continue
		}
		if got := stopReason(t, m); got != tc.want {
			t.Errorf("%s: stopReason %q", tc.want, got)
		}
	}
}

// session/load in a new server replays the saved history before it answers.
func TestSessionLoadReplaysTheHistory(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(`[
	  {"text": "Running.", "calls": [{"name": "bash", "arguments": {"command": "echo hi"}}]},
	  {"text": "first answer"}
	]`), 0o644)
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}
	first := serveWith(t, o)
	first.init()
	session := first.newSession(dir)
	stopReason(t, func() msg { m, _ := first.prompt(session, "question one"); return m }())

	second := serveWith(t, o)
	second.init()
	if m, _ := second.call("session/load", map[string]any{"sessionId": "gilda-0000", "cwd": dir, "mcpServers": []any{}}); m.Error == nil || m.Error.Code != codeNotFound {
		t.Errorf("unknown session: %+v", m)
	}
	m, updates := second.call("session/load", map[string]any{"sessionId": session, "cwd": dir, "mcpServers": []any{}})
	if m.Error != nil {
		t.Fatalf("session/load: %v", m.Error)
	}
	if got, want := kinds(updates), "user_message_chunk agent_message_chunk tool_call:completed agent_message_chunk"; got != want {
		t.Fatalf("replay\n got %s\nwant %s", got, want)
	}
	if text := updates[0].Update.Content.(map[string]any)["text"]; text != "question one" {
		t.Errorf("user text %v", text)
	}
	// The loaded session takes prompts; its app read the mock script afresh.
	if m, _ := second.prompt(session, "again"); stopReason(t, m) != "end_turn" {
		t.Errorf("prompt after load: %+v", m)
	}
}

// Without a trusted checkout, a session asks before bash, as -p --json does.
func TestUntrustedCheckoutAsks(t *testing.T) {
	c, dir := serve(t, `[{"calls": [{"name": "bash", "arguments": {"command": "true"}}]}, {"text": "ok"}]`,
		func(o *app.Options) { o.Permissions = "" })
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644)
	c.init()
	m, updates := c.prompt(c.newSession(dir), "go")
	stopReason(t, m)
	c.mu.Lock()
	asked := len(c.asked)
	c.mu.Unlock()
	if asked != 1 || !strings.Contains(kinds(updates), "tool_call_update:failed") {
		t.Errorf("asked %d; updates %s", asked, kinds(updates))
	}
}

// With the model's window unknown, no usage_update is sent: its size is required, and 0 would
// claim an empty window.
func TestNoUsageUpdateWithoutAWindow(t *testing.T) {
	c, dir := serve(t, costedTurn)
	c.init()
	m, updates := c.prompt(c.newSession(dir), "go")
	if got := stopReason(t, m); got != "end_turn" {
		t.Fatalf("stopReason %q", got)
	}
	if got := kinds(updates); strings.Contains(got, "usage_update") {
		t.Fatalf("a usage_update was sent without a window: %s", got)
	}
}

// A cancel in the same write as its prompt stops that prompt before its tool runs.
func TestACancelInThePromptsWriteEndsItCancelled(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	script := fmt.Sprintf(`[{"calls": [{"name": "bash", "arguments": {"command": "touch %s"}}]}, {"text": "ran"}]`, marker)
	c, dir := serve(t, script)
	c.init()
	for i := range 20 {
		session := c.newSession(dir)
		id, line := c.request("session/prompt", textPrompt(session, "go"))
		c.writeLine(append(line, cancelLine(session)...))
		m, _ := c.wait(id)
		if got := stopReason(t, m); got != "cancelled" {
			t.Fatalf("run %d: stopReason %q", i, got)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("run %d: the tool ran after its cancel", i)
		}
	}
}

// A prompt sent just after a cancel waits for the cancelled one and then runs.
func TestAPromptAfterACancelWaitsForIt(t *testing.T) {
	c, dir := serve(t, `[{"calls": [{"name": "bash", "arguments": {"command": "sleep 30"}}]}, {"text": "next"}, {"text": "next"}]`)
	c.init()
	session := c.newSession(dir)
	first := c.send("session/prompt", textPrompt(session, "wait"))
	time.Sleep(300 * time.Millisecond)
	second, line := c.request("session/prompt", textPrompt(session, "again"))
	c.writeLine(append(cancelLine(session), line...))
	if m, _ := c.wait(first); stopReason(t, m) != "cancelled" {
		t.Errorf("first prompt: %s", m.Result)
	}
	if m, _ := c.wait(second); stopReason(t, m) != "end_turn" {
		t.Errorf("second prompt: %s", m.Result)
	}
	// One that does not follow a cancel is still refused while a prompt runs.
	c2, dir2 := serve(t, `[{"calls": [{"name": "bash", "arguments": {"command": "sleep 30"}}]}, {"text": "x"}]`)
	c2.init()
	s2 := c2.newSession(dir2)
	c2.send("session/prompt", textPrompt(s2, "wait"))
	time.Sleep(300 * time.Millisecond)
	if m, _ := c2.prompt(s2, "again"); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("a concurrent prompt: %+v", m)
	}
	c2.writeLine(cancelLine(s2))
}

// Two session/load calls for one id: only one opens it.
func TestConcurrentLoadsOpenTheSessionOnce(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(`[{"text": "a"}]`), 0o644)
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}
	first := serveWith(t, o)
	first.init()
	session := first.newSession(dir)
	stopReason(t, func() msg { m, _ := first.prompt(session, "q"); return m }())

	second := serveWith(t, o)
	second.init()
	params := map[string]any{"sessionId": session, "cwd": dir, "mcpServers": []any{}}
	a, la := second.request("session/load", params)
	b, lb := second.request("session/load", params)
	second.writeLine(append(la, lb...))
	// Either reply may come first; wait would drop the other.
	var ma, mb msg
	timeout := time.After(10 * time.Second)
	for ma.ID == nil || mb.ID == nil {
		select {
		case m, ok := <-second.msgs:
			if !ok {
				t.Fatal("output ended before both load replies")
			}
			switch string(m.ID) {
			case fmt.Sprint(a):
				ma = m
			case fmt.Sprint(b):
				mb = m
			}
		case <-timeout:
			t.Fatal("no reply to both loads within 10 s")
		}
	}
	ok := 0
	for _, m := range []msg{ma, mb} {
		if m.Error == nil {
			ok++
		} else if m.Error.Code != codeInvalidRequest {
			t.Errorf("refused load: %v", m.Error)
		}
	}
	if ok != 1 {
		t.Fatalf("%d of 2 loads succeeded, want 1", ok)
	}
}

// The cost a session reports goes on from its saved cost after session/load.
func TestTheCostGoesOnAfterALoad(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(costedTurn), 0o644)
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state, Context: 200000}
	first := serveWith(t, o)
	first.init()
	session := first.newSession(dir)
	stopReason(t, func() msg { m, _ := first.prompt(session, "q"); return m }())

	second := serveWith(t, o)
	second.init()
	// A trailing slash names the same cwd.
	if m, _ := second.call("session/load", map[string]any{"sessionId": session, "cwd": dir + "/", "mcpServers": []any{}}); m.Error != nil {
		t.Fatalf("session/load: %v", m.Error)
	}
	m, updates := second.prompt(session, "again")
	stopReason(t, m)
	last := updates[len(updates)-1].Update
	if last.Cost == nil || fmt.Sprintf("%.4f", last.Cost.Amount) != "0.0060" {
		t.Errorf("cost after the load %+v, want 0.0060", last.Cost)
	}
}

// After a save forks the session, its new id names it too, so loading that id is refused.
func TestAForkedSessionKeepsItsNewID(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(`[{"text": "a"}, {"text": "b"}, {"text": "c"}]`), 0o644)
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}
	first := serveWith(t, o)
	first.init()
	session := first.newSession(dir)
	stopReason(t, func() msg { m, _ := first.prompt(session, "q"); return m }())
	second := serveWith(t, o)
	second.init()
	if m, _ := second.call("session/load", map[string]any{"sessionId": session, "cwd": dir, "mcpServers": []any{}}); m.Error != nil {
		t.Fatalf("session/load: %v", m.Error)
	}
	stopReason(t, func() msg { m, _ := second.prompt(session, "q2"); return m }())
	// first saves after second did, so its conversation forks to a new id.
	time.Sleep(10 * time.Millisecond)
	stopReason(t, func() msg { m, _ := first.prompt(session, "q3"); return m }())
	list, _ := app.Sessions(state, dir)
	var forked string
	for _, info := range list {
		if info.ID != session {
			forked = info.ID
		}
	}
	if forked == "" {
		t.Fatalf("no forked session in %+v", list)
	}
	if m, _ := first.call("session/load", map[string]any{"sessionId": forked, "cwd": dir, "mcpServers": []any{}}); m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("loading the forked id in its own server: %+v", m)
	}
	if m, _ := first.prompt(forked, "q4"); m.Error != nil {
		t.Errorf("a prompt by the forked id: %v", m.Error)
	}
}

// A session cwd outside the bound is refused.
func TestTheBoundRefusesACwdOutsideIt(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(`[]`), 0o644)
	c := serveOpts(t, Options{Bound: dir + "/", App: app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}})
	c.init()
	c.newSession(sub + "/")
	c.newSession(dir)
	for _, cwd := range []string{t.TempDir(), sub + "/../.."} {
		if m, _ := c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}); m.Error == nil || m.Error.Code != codeInvalidParams {
			t.Errorf("cwd %s: %+v", cwd, m)
		}
	}
	link := filepath.Join(dir, "out")
	os.Symlink(t.TempDir(), link)
	if m, _ := c.call("session/new", map[string]any{"cwd": link, "mcpServers": []any{}}); m.Error == nil || m.Error.Code != codeInvalidParams {
		t.Errorf("a symlink out of the bound: %+v", m)
	}
}

// A JSON-RPC batch is an invalid request, not a parse error.
func TestABatchIsAnInvalidRequest(t *testing.T) {
	c, _ := serve(t, `[]`)
	c.writeLine([]byte(`[{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}]` + "\n"))
	if m := <-c.msgs; m.Error == nil || m.Error.Code != codeInvalidRequest {
		t.Errorf("batch: %+v", m)
	}
}

// Valid JSON that is not a request object is an invalid request with a null id.
func TestANonObjectIsAnInvalidRequest(t *testing.T) {
	c, _ := serve(t, `[]`)
	for _, line := range []string{`null`, `{}`, `123`, `"x"`} {
		c.writeLine([]byte(line + "\n"))
		select {
		case m := <-c.msgs:
			if m.Error == nil || m.Error.Code != codeInvalidRequest || string(m.ID) != "null" {
				t.Errorf("%s: %+v", line, m)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no reply", line)
		}
	}
}

// A prompt without a sessionId has invalid params, not an unknown session.
func TestAPromptWithoutParamsIsInvalid(t *testing.T) {
	c, _ := serve(t, `[]`)
	c.init()
	for _, params := range []any{nil, map[string]any{}} {
		if m, _ := c.call("session/prompt", params); m.Error == nil || m.Error.Code != codeInvalidParams {
			t.Errorf("params %v: %+v", params, m)
		}
	}
}

// A prompt that arrives while session/load replays the history is refused: it would append
// to the history the replay reads.
func TestAPromptDuringTheReplayIsRefused(t *testing.T) {
	state, dir := t.TempDir(), t.TempDir()
	mock := filepath.Join(state, "mock.json")
	os.WriteFile(mock, []byte(`[{"text": "a"}]`), 0o644)
	o := app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state}
	first := serveWith(t, o)
	first.init()
	session := first.newSession(dir)
	stopReason(t, func() msg { m, _ := first.prompt(session, "q"); return m }())
	// A history longer than the client's buffer, so the replay blocks until the client reads.
	path := filepath.Join(state, "sessions", session+".json")
	var saved map[string]any
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &saved) != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	msgs := saved["messages"].([]any)
	for len(msgs) < 2*cap(first.msgs) {
		msgs = append(msgs, msgs[0])
	}
	saved["messages"] = msgs
	data, _ = json.Marshal(saved)
	os.WriteFile(path, data, 0o644)

	second := serveWith(t, o)
	second.init()
	load := second.send("session/load", map[string]any{"sessionId": session, "cwd": dir, "mcpServers": []any{}})
	for deadline := time.Now().Add(10 * time.Second); len(second.msgs) < cap(second.msgs); {
		if time.Now().After(deadline) {
			t.Fatal("the replay did not fill the client's buffer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	prompt := second.send("session/prompt", textPrompt(session, "go"))
	time.Sleep(100 * time.Millisecond)
	// The refusal may come before the load's reply.
	replies := map[string]msg{}
	for timeout := time.After(10 * time.Second); len(replies) < 2; {
		select {
		case m, ok := <-second.msgs:
			if !ok {
				t.Fatal("output ended before both replies")
			}
			if m.Method == "" {
				replies[string(m.ID)] = m
			}
		case <-timeout:
			t.Fatal("no reply to both requests within 10 s")
		}
	}
	if m := replies[fmt.Sprint(load)]; m.Error != nil {
		t.Fatalf("session/load: %v", m.Error)
	}
	if m := replies[fmt.Sprint(prompt)]; m.Error == nil || m.Error.Code != codeNotFound {
		t.Fatalf("a prompt during the replay: %+v", m)
	}
}

// A resent response sends only what the client has not seen: a repeated prefix is skipped,
// and an answer that differs follows a blank line. The notice goes to the log.
func TestAResentResponseIsNotRepeated(t *testing.T) {
	cases := []struct{ cut, whole, want string }{
		{"The ans", "The answer.", "The answer."},
		{"The answer.", "The answer.", "The answer."},
		{"Hello wor", "Hi there.", "Hello wor\n\nHi there."},
		{"The answer is long", "The answer.", "The answer is long\n\nThe answer."},
		{"The answer. More", "The answer.", "The answer. More\n\nThe answer."},
	}
	for _, tc := range cases {
		state := t.TempDir()
		mock := filepath.Join(state, "mock.json")
		script, _ := json.Marshal([]map[string]any{{"text": tc.cut, "incomplete": true}, {"text": tc.whole}})
		os.WriteFile(mock, script, 0o644)
		var log strings.Builder
		var mu sync.Mutex
		c := serveOpts(t, Options{App: app.Options{Mock: mock, Permissions: "auto", StateDir: state, ConfigDir: state, CacheDir: state},
			Log: writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return log.Write(p) })})
		c.init()
		m, updates := c.prompt(c.newSession(t.TempDir()), "go")
		stopReason(t, m)
		var got strings.Builder
		for _, u := range updates {
			if u.Update.SessionUpdate == "agent_message_chunk" {
				got.WriteString(u.Update.Content.(map[string]any)["text"].(string))
			}
		}
		if got.String() != tc.want {
			t.Errorf("%q then %q: the client saw %q, want %q", tc.cut, tc.whole, got.String(), tc.want)
		}
		mu.Lock()
		if !strings.Contains(log.String(), "cut off") {
			t.Errorf("no notice in the log: %q", log.String())
		}
		mu.Unlock()
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
