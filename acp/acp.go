// Package acp serves gilda as an Agent Client Protocol agent, version 1: JSON-RPC 2.0, one
// message per line, on a reader and a writer (https://agentclientprotocol.com/protocol/). It is
// a front end like the REPL and -p: each session is an app.App in the client's cwd, with the
// same agent loop, tools, permission modes and saved sessions.
//
// A call that the mode would ask the user about is sent to the client as
// session/request_permission. The client's file system and terminal are never used.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/shakfu/gilda/agent"
	"github.com/shakfu/gilda/app"
	"github.com/shakfu/gilda/llm"
	"github.com/shakfu/gilda/permission"
	"github.com/shakfu/gilda/tool"
)

// ProtocolVersion is the ACP version served.
const ProtocolVersion = 1

// JSON-RPC and ACP error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeNotFound       = -32002
)

type Options struct {
	// App is the template for each session's app; Root, Resume, Continue and Ask are set per
	// session.
	App     app.Options
	Version string
	// Bound, when set, is the directory every session cwd must be in.
	Bound string
	// Log receives notes and warnings, such as os.Stderr. Nil discards them.
	Log io.Writer
}

// Serve answers the client on in and out until in ends or ctx is cancelled. Running prompts are
// then cancelled and saved.
func Serve(ctx context.Context, in io.Reader, out io.Writer, o Options) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &server{opts: o, ctx: ctx, stop: cancel, out: out, sessions: map[string]*session{}, loading: map[string]bool{},
		pending: map[int64]chan message{}}
	defer func() {
		cancel()
		s.wg.Wait()
		closed := map[*session]bool{}
		for _, sess := range s.sessions {
			if !closed[sess] {
				closed[sess] = true
				sess.app.Close()
			}
		}
	}()
	// Read apart from handling, so a cancelled ctx ends Serve while the read blocks.
	lines, done := make(chan []byte), make(chan error, 1)
	go func() {
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	for {
		select {
		case line := <-lines:
			s.handle(line)
		case err := <-done:
			if err == io.EOF {
				return nil
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

type server struct {
	opts Options
	ctx  context.Context
	// stop ends Serve; a failed write calls it, since the client is gone.
	stop context.CancelFunc
	out  io.Writer
	wmu  sync.Mutex // serializes writes, one message per line
	wg   sync.WaitGroup
	// initialized is read and written only by the reading goroutine.
	initialized bool

	mu sync.Mutex
	// sessions maps each id to its session; a forked save adds the new id as a second key.
	sessions map[string]*session
	// loading holds the ids a session/load is opening.
	loading map[string]bool
	nextID  int64
	pending map[int64]chan message
	// seq numbers the prompts in session.stops.
	seq int64
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

func fail(code int, format string, args ...any) *rpcError {
	return &rpcError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// send writes one message. json.Marshal escapes newlines inside strings, so it is one line.
func (s *server) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(s.opts.Log, "gilda: acp:", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.out.Write(append(data, '\n')); err != nil {
		// Nobody reads the answers: stop, so running prompts are cancelled and saved.
		s.stop()
	}
}

func (s *server) reply(id json.RawMessage, result any, err *rpcError) {
	if id == nil {
		id = json.RawMessage("null")
	}
	if err != nil {
		s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": err})
		return
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) notify(method string, params any) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// call sends a request to the client and waits for its result.
func (s *server) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan message, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handle dispatches one line. Requests that may take long run in their own goroutine, so
// session/cancel and the client's answers are read while a prompt runs.
func (s *server) handle(line []byte) {
	t := bytes.TrimSpace(line)
	var m message
	err := json.Unmarshal(t, &m)
	switch {
	case !json.Valid(t):
		s.reply(nil, nil, fail(codeParse, "parse error: %v", err))
		return
	case t[0] == '[':
		s.reply(nil, nil, fail(codeInvalidRequest, "gilda does not take JSON-RPC batches"))
		return
	case t[0] != '{' || err != nil:
		s.reply(nil, nil, fail(codeInvalidRequest, "a message must be a JSON-RPC 2.0 object"))
		return
	}
	// Without "jsonrpc" it is no notification either, so it gets an answer.
	if m.JSONRPC != "2.0" {
		s.reply(m.ID, nil, fail(codeInvalidRequest, `jsonrpc must be "2.0"`))
		return
	}
	notification := len(m.ID) == 0 || string(m.ID) == "null"
	if m.Method == "" {
		s.answer(m)
		return
	}
	if notification {
		if m.Method == "session/cancel" && s.initialized {
			s.cancel(m.Params)
		}
		return
	}
	if m.Method == "initialize" {
		s.initialized = true
		s.reply(m.ID, s.initialize(), nil)
		return
	}
	if !s.initialized {
		s.reply(m.ID, nil, fail(codeInvalidRequest, "call initialize first"))
		return
	}
	var run func(json.RawMessage) (any, *rpcError)
	switch m.Method {
	case "session/new":
		run = s.newSession
	case "session/load":
		run = s.loadSession
	case "session/prompt":
		// Tracked before the goroutine starts, so a session/cancel on the next line stops it.
		var p promptParams
		if err := decode(m.Params, &p); err != nil {
			s.reply(m.ID, nil, err)
			return
		}
		if p.SessionID == "" {
			s.reply(m.ID, nil, fail(codeInvalidParams, "sessionId is required"))
			return
		}
		sess, err := s.session(p.SessionID)
		if err != nil {
			s.reply(m.ID, nil, err)
			return
		}
		ctx, done := sess.track()
		run = func(json.RawMessage) (any, *rpcError) {
			defer done()
			return sess.prompt(ctx, p)
		}
	default:
		s.reply(m.ID, nil, fail(codeMethodNotFound, "method not found: %s", m.Method))
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		result, err := run(m.Params)
		s.reply(m.ID, result, err)
	}()
}

// answer passes a response to the call waiting for it.
func (s *server) answer(m message) {
	var id int64
	if json.Unmarshal(m.ID, &id) != nil {
		return
	}
	s.mu.Lock()
	ch := s.pending[id]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m: // a duplicate answer is dropped
		default:
		}
	}
}

func (s *server) initialize() any {
	version := s.opts.Version
	if version == "" {
		version = "dev"
	}
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"agentCapabilities": map[string]any{
			"loadSession":        true,
			"promptCapabilities": map[string]bool{"image": false, "audio": false, "embeddedContext": false},
			"mcpCapabilities":    map[string]bool{"http": false, "sse": false},
		},
		"agentInfo":   map[string]string{"name": "gilda", "version": version},
		"authMethods": []any{},
	}
}

type sessionParams struct {
	SessionID  string            `json:"sessionId"`
	Cwd        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

func (s *server) newSession(raw json.RawMessage) (any, *rpcError) {
	var p sessionParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	sess, err := s.open(p, "")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[sess.id] = sess
	s.mu.Unlock()
	return map[string]string{"sessionId": sess.id}, nil
}

// loadSession resumes a saved session and replays its history before answering.
func (s *server) loadSession(raw json.RawMessage) (any, *rpcError) {
	var p sessionParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.SessionID == "" {
		return nil, fail(codeInvalidParams, "sessionId is required")
	}
	s.mu.Lock()
	_, open := s.sessions[p.SessionID]
	busy := open || s.loading[p.SessionID]
	if !busy {
		s.loading[p.SessionID] = true
	}
	s.mu.Unlock()
	if busy {
		return nil, fail(codeInvalidRequest, "session %s is already open", p.SessionID)
	}
	defer func() {
		s.mu.Lock()
		delete(s.loading, p.SessionID)
		s.mu.Unlock()
	}()
	if err := s.checkCwd(&p); err != nil {
		return nil, err
	}
	// An exact id only: app.New also takes a prefix, which would load another session.
	list, lerr := app.Sessions(s.opts.App.StateDir, p.Cwd)
	if lerr != nil {
		return nil, fail(codeInternal, "%v", lerr)
	}
	found := false
	for _, info := range list {
		found = found || info.ID == p.SessionID
	}
	if !found {
		return nil, fail(codeNotFound, "no saved session %s for %s", p.SessionID, p.Cwd)
	}
	sess, err := s.open(p, p.SessionID)
	if err != nil {
		return nil, err
	}
	// Registered only after the replay, so no prompt runs on the history while it is read.
	sess.replay()
	s.mu.Lock()
	s.sessions[sess.id] = sess
	delete(s.loading, p.SessionID)
	s.mu.Unlock()
	return map[string]any{}, nil
}

// checkCwd cleans p.Cwd and refuses one that is not an absolute directory within the bound.
func (s *server) checkCwd(p *sessionParams) *rpcError {
	if !filepath.IsAbs(p.Cwd) {
		return fail(codeInvalidParams, "cwd must be an absolute path, not %q", p.Cwd)
	}
	p.Cwd = filepath.Clean(p.Cwd)
	if fi, err := os.Stat(p.Cwd); err != nil || !fi.IsDir() {
		return fail(codeInvalidParams, "cwd %s is not a directory", p.Cwd)
	}
	if b := s.opts.Bound; b != "" && !within(b, p.Cwd) {
		return fail(codeInvalidParams, "cwd %s is outside %s; start gilda with --root to allow it", p.Cwd, b)
	}
	return nil
}

// within reports whether dir is root or below it, once symlinks are resolved.
func within(root, dir string) bool {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// open builds a session's app in p.Cwd, resuming id when it is set. The caller registers it.
func (s *server) open(p sessionParams, id string) (*session, *rpcError) {
	if err := s.checkCwd(&p); err != nil {
		return nil, err
	}
	if len(p.MCPServers) > 0 {
		fmt.Fprintf(s.opts.Log, "gilda: ignoring %d MCP servers from the client; gilda has no MCP support\n", len(p.MCPServers))
	}
	o := s.opts.App
	o.Root, o.Resume, o.Continue, o.Ask = p.Cwd, id, false, nil
	a, err := app.New(o)
	if err != nil {
		return nil, fail(codeInternal, "%v", err)
	}
	sess := &session{srv: s, app: a, stops: map[int64]context.CancelFunc{}, always: map[permission.Allowance]bool{}, resent: -1}
	a.SetPermissions("", sess.ask)
	// Nobody can answer the trust question here, so the session uses ask mode, as -p --json does.
	if dir, _ := a.Trust(); dir != "" {
		a.SetPermissions(permission.Ask, a.Ask())
		fmt.Fprintf(s.opts.Log, "gilda: instructions in %s are not trusted yet, so this session uses ask mode; "+
			"answer in the REPL, or pass --permissions auto\n", dir)
	}
	sess.watchApprovals()
	warns, err := a.Prepare(s.ctx)
	for _, w := range warns {
		fmt.Fprintln(s.opts.Log, "gilda: warning:", w)
	}
	if err != nil {
		a.Close()
		return nil, fail(codeInternal, "%v", err)
	}
	sess.id = a.Agent.SessionID
	return sess, nil
}

func (s *server) session(id string) (*session, *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	switch {
	case !ok && s.loading[id]:
		return nil, fail(codeNotFound, "session %s is still loading", id)
	case !ok:
		return nil, fail(codeNotFound, "no session %q", id)
	}
	return sess, nil
}

func (s *server) cancel(raw json.RawMessage) {
	var p sessionParams
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[p.SessionID]; sess != nil {
		for _, stop := range sess.stops {
			stop()
		}
	}
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URI  string `json:"uri"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// prompt runs one session/prompt; ctx is cancelled by session/cancel.
func (s *session) prompt(ctx context.Context, p promptParams) (any, *rpcError) {
	var parts []string
	for _, b := range p.Prompt {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource_link":
			parts = append(parts, b.URI)
		default:
			return nil, fail(codeInvalidParams, "gilda takes text and resource_link blocks, not %q", b.Type)
		}
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		return nil, fail(codeInvalidParams, "the prompt is empty")
	}
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()
	return s.run(ctx, text)
}

// track registers a prompt for session/cancel. done unregisters it.
func (s *session) track() (ctx context.Context, done func()) {
	ctx, stop := context.WithCancel(s.srv.ctx)
	s.srv.mu.Lock()
	s.srv.seq++
	k := s.srv.seq
	s.stops[k] = stop
	s.srv.mu.Unlock()
	return ctx, func() {
		s.srv.mu.Lock()
		delete(s.stops, k)
		s.srv.mu.Unlock()
		stop()
	}
}

// acquire makes ctx's prompt the session's turn. It waits for a cancelled turn to end, and
// refuses while one that is not cancelled runs.
func (s *session) acquire(ctx context.Context) *rpcError {
	for {
		s.srv.mu.Lock()
		if s.turn == nil {
			s.turn, s.idle = ctx, make(chan struct{})
			s.srv.mu.Unlock()
			return nil
		}
		cancelled, idle := s.turn.Err() != nil, s.idle
		s.srv.mu.Unlock()
		if !cancelled {
			return fail(codeInvalidRequest, "a prompt is already running in session %s", s.id)
		}
		<-idle
	}
}

func (s *session) release() {
	s.srv.mu.Lock()
	s.turn = nil
	close(s.idle)
	s.srv.mu.Unlock()
}

func decode(raw json.RawMessage, v any) *rpcError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fail(codeInvalidParams, "invalid params: %v", err)
	}
	return nil
}

// session is one ACP session: an app and what the client allowed for always.
type session struct {
	srv *server
	id  string
	app *app.App
	// stops cancels each prompt read and not yet answered. turn is the running prompt's
	// context; idle closes when it ends. srv.mu guards all three.
	stops map[int64]context.CancelFunc
	turn  context.Context
	idle  chan struct{}
	// always is what allow_always answers allowed, as the REPL's "a" does. Only the prompt's
	// goroutine reads and writes it.
	always map[permission.Allowance]bool
	// shown is the round-trip's text the client has seen. After a resend, resent counts the
	// bytes of shown the new attempt repeated so far; it is -1 otherwise.
	shown  string
	resent int
}

func (s *session) update(u map[string]any) {
	s.srv.notify("session/update", map[string]any{"sessionId": s.id, "update": u})
}

func textUpdate(kind, text string) map[string]any {
	return map[string]any{"sessionUpdate": kind, "content": map[string]string{"type": "text", "text": text}}
}

// run sends one prompt through the agent and returns its stop reason.
func (s *session) run(ctx context.Context, text string) (any, *rpcError) {
	if ctx.Err() != nil {
		return map[string]string{"stopReason": "cancelled"}, nil
	}
	s.shown, s.resent = "", -1
	res, err := s.app.Agent.Run(ctx, text, s.event)
	if serr := s.app.Save(); serr != nil {
		fmt.Fprintln(s.srv.opts.Log, "gilda: warning: session:", serr)
	}
	// A forked save renamed the conversation; the new id also names this session.
	if id := s.app.Agent.SessionID; id != s.id {
		s.srv.mu.Lock()
		s.srv.sessions[id] = s
		s.srv.mu.Unlock()
	}
	reason := "end_turn"
	switch {
	case ctx.Err() != nil:
		reason = "cancelled"
	case errors.Is(err, agent.ErrMaxTurns):
		reason = "max_turn_requests"
	case res.Stop == llm.StopRefusal:
		reason = "refusal"
	case err != nil:
		return nil, fail(codeInternal, "%v", err)
	case res.Stop == llm.StopMaxTokens:
		reason = "max_tokens"
	}
	return map[string]string{"stopReason": reason}, nil
}

// event turns an agent event into session/update notifications.
func (s *session) event(e agent.Event) {
	log := s.srv.opts.Log
	switch e := e.(type) {
	case agent.Text:
		s.text(e.Text)
	case agent.Reasoning:
		s.update(textUpdate("agent_thought_chunk", e.Text))
	case agent.ToolCall:
		u := s.toolCall(e.Call, e.Label)
		u["sessionUpdate"], u["status"] = "tool_call", "pending"
		s.update(u)
	case agent.ToolResult:
		s.update(s.toolResult(e.Call.ID, e.Result, e.Err))
	case agent.Retry:
		if s.shown != "" {
			s.resent = 0
			fmt.Fprintln(log, "gilda: the response was cut off; sending it again")
		}
		fmt.Fprintf(log, "gilda: retry %d: %s\n", e.Attempt, e.Reason)
	case agent.Elided:
		fmt.Fprintf(log, "gilda: elided %d old tool results, %d bytes\n", e.Results, e.Bytes)
	case agent.Response:
		// A resend that stopped short of what the client saw differs from it.
		if s.resent > 0 && s.resent < len(s.shown) {
			s.update(textUpdate("agent_message_chunk", "\n\n"+s.shown[:s.resent]))
		}
		s.shown, s.resent = "", -1
		s.app.Remember()
		s.sendUsage()
	case agent.TaskDone:
		s.sendUsage()
	}
}

// text sends a text delta. The client cannot take back what it showed, so a resend skips the
// prefix it repeats; one that differs follows a blank line, from its start.
func (s *session) text(t string) {
	if s.resent >= 0 {
		rest := s.shown[s.resent:]
		n := 0
		for n < len(t) && n < len(rest) && t[n] == rest[n] {
			n++
		}
		switch {
		case n == len(t):
			s.resent += n
			return
		case n == len(rest):
			t = t[n:]
		default:
			s.shown = s.shown[:s.resent] + t
			s.resent = -1
			s.update(textUpdate("agent_message_chunk", "\n\n"+s.shown))
			return
		}
		s.resent = -1
	}
	s.shown += t
	s.update(textUpdate("agent_message_chunk", t))
}

// toolCall is the fields a tool_call shares with a permission request's toolCall.
func (s *session) toolCall(c llm.ToolCall, label string) map[string]any {
	u := map[string]any{"toolCallId": c.ID, "title": label, "kind": "other"}
	t := tool.Find(s.app.Agent.Tools, c.Name)
	args := json.RawMessage(c.Arguments)
	if json.Valid(args) {
		u["rawInput"] = args
	}
	if t == nil {
		return u
	}
	u["kind"] = kind(c.Name, t)
	if pt, ok := t.(tool.Paths); ok {
		if paths, err := pt.Paths(args); err == nil && len(paths) > 0 {
			var locs []map[string]string
			for _, p := range paths {
				if !filepath.IsAbs(p) {
					p = filepath.Join(s.app.Root(), p)
				}
				locs = append(locs, map[string]string{"path": p})
			}
			u["locations"] = locs
		}
	}
	return u
}

func (s *session) toolResult(id string, r tool.Result, err error) map[string]any {
	status, text := "completed", tool.Cap(r.Output, s.app.Agent.OutputCap)
	if err != nil {
		status, text = "failed", err.Error()
	} else if r.Failed {
		status = "failed"
	}
	u := map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": id, "status": status}
	if text != "" {
		u["content"] = []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": text}}}
	}
	return u
}

// kind is the ACP tool kind of a call.
func kind(name string, t tool.Tool) string {
	switch name {
	case "read":
		return "read"
	case "write", "edit", "apply_patch":
		return "edit"
	case "bash":
		return "execute"
	}
	if ro, ok := t.(tool.ReadOnly); ok && ro.ReadOnly() {
		return "read"
	}
	return "other"
}

// sendUsage reports the context filled and the session's cost so far. With the window
// unknown it sends nothing: usage_update requires size, and 0 would claim an empty window.
func (s *session) sendUsage() {
	if u := s.usage(); u != nil {
		s.update(u)
	}
}

// usage is the session's usage_update, or nil while the model's window is unknown.
func (s *session) usage() map[string]any {
	ag := s.app.Agent
	if ag.Context <= 0 {
		return nil
	}
	u := map[string]any{"sessionUpdate": "usage_update", "used": ag.Used, "size": ag.Context}
	if c := s.app.SessionCost(); c != nil {
		u["cost"] = map[string]any{"amount": *c, "currency": "USD"}
	}
	return u
}

// watchApprovals marks a call in_progress once it is approved, whether or not the client was
// asked. A subagent copies Approve, so its calls are marked too.
func (s *session) watchApprovals() {
	approve := s.app.Agent.Approve
	s.app.Agent.Approve = func(ctx context.Context, t tool.Tool, c llm.ToolCall, label string) (bool, error) {
		ok, err := approve(ctx, t, c, label)
		if ok && err == nil {
			s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": c.ID, "status": "in_progress"})
		}
		return ok, err
	}
}

// ask is the permission.AskFunc of a session: it asks the client. Any answer but an allow
// option, a cancelled request included, refuses the call.
func (s *session) ask(ctx context.Context, t tool.Tool, call llm.ToolCall, label string, why permission.Reason) (bool, error) {
	need := why.Allowances(call.Name)
	covered := len(need) > 0
	for _, a := range need {
		covered = covered && s.always[a]
	}
	if covered {
		return true, nil
	}
	tc := s.toolCall(call, label)
	tc["status"] = "pending"
	text := "needs approval: " + why.Text
	if p := s.app.Preview(t, call); p != "" {
		text += "\n\n" + p
	}
	tc["content"] = []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": text}}}
	options := []map[string]string{{"optionId": "allow_once", "name": "Allow", "kind": "allow_once"}}
	if len(need) > 0 {
		options = append(options, map[string]string{"optionId": "allow_always",
			"name": "Always allow " + why.Scope(call.Name) + " in this session", "kind": "allow_always"})
	}
	options = append(options, map[string]string{"optionId": "reject_once", "name": "Reject", "kind": "reject_once"})

	raw, err := s.srv.call(ctx, "session/request_permission", map[string]any{"sessionId": s.id, "toolCall": tc, "options": options})
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("refused: the client did not answer the permission request: %v", err)
	}
	var r struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(raw, &r)
	if r.Outcome.Outcome != "selected" {
		return false, nil
	}
	switch r.Outcome.OptionID {
	case "allow_always":
		for _, a := range need {
			s.always[a] = true
		}
		return true, nil
	case "allow_once":
		return true, nil
	}
	return false, nil
}

// replay sends the loaded history as updates: prompts, replies, and tool calls with their
// results.
func (s *session) replay() {
	results := map[string]llm.ToolResult{}
	for _, m := range s.app.Agent.History {
		for _, r := range m.Results {
			results[r.CallID] = r
		}
	}
	for _, m := range s.app.Agent.History {
		switch {
		case m.Role == llm.User && m.Text != "":
			s.update(textUpdate("user_message_chunk", m.Text))
		case m.Role == llm.Assistant:
			if m.Text != "" {
				s.update(textUpdate("agent_message_chunk", m.Text))
			}
			for _, c := range m.Calls {
				label := c.Name
				if t := tool.Find(s.app.Agent.Tools, c.Name); t != nil {
					label = t.Label(json.RawMessage(c.Arguments))
				}
				u := s.toolCall(c, label)
				u["sessionUpdate"], u["status"] = "tool_call", "completed"
				if r, ok := results[c.ID]; ok {
					if r.IsError {
						u["status"] = "failed"
					}
					u["content"] = []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": r.Content}}}
				}
				s.update(u)
			}
		}
	}
}
