package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shakfu/gilda/llm/llmtest"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gilda-test")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "gilda")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// testEnv is the environment without the user's GILDA_ variables, which would change what
// the binary does (GILDA_PERMISSIONS, GILDA_PROVIDER) or where it writes (GILDA_LOG), plus extra.
func testEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GILDA_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// gilda runs the binary in a scratch directory with its own state, and returns stdout, stderr
// and the exit status.
func gilda(t *testing.T, script string, args ...string) (string, string, int) {
	t.Helper()
	return gildaIn(t, t.TempDir(), script, args...)
}

// gildaIn is gilda in dir, so runs can share state.
func gildaIn(t *testing.T, dir, script string, args ...string) (string, string, int) {
	t.Helper()
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "mock.json"), []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append([]string{"--mock", "mock.json"}, args...)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

const script = `[
  {"text": "Listing.", "calls": [{"name": "bash", "arguments": {"command": "echo hi"}}], "usage": {"input_tokens": 10, "output_tokens": 2, "cost": 0.001}},
  {"text": "The answer.", "usage": {"input_tokens": 20, "output_tokens": 3, "cost": 0.002}}
]`

func TestHeadlessPrintsOnlyTheAnswerOnStdout(t *testing.T) {
	stdout, stderr, code := gilda(t, script, "-p", "go")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if stdout != "Listing.\nThe answer.\n" {
		t.Fatalf("stdout %q", stdout)
	}
	if !strings.Contains(stderr, "$ echo hi -> hi") || !strings.Contains(stderr, "$0.0030") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestJSONEndsInAResultRecord(t *testing.T) {
	stdout, _, code := gilda(t, script, "-p", "go", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var types []string
	var last map[string]any
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("not JSON: %q", sc.Text())
		}
		types = append(types, rec["type"].(string))
		last = rec
	}
	want := "start turn tool_call tool_result turn result"
	if strings.Join(types, " ") != want {
		t.Fatalf("records %v", types)
	}
	usage := last["usage"].(map[string]any)
	if last["outcome"] != "complete" || last["text"] != "The answer." || usage["cost"] != 0.003 || last["turns"] != float64(2) {
		t.Fatalf("result %v", last)
	}
}

func TestErrorsExitOneAndAppearInTheResult(t *testing.T) {
	stdout, _, code := gilda(t, `[{"error": "upstream exploded"}]`, "-p", "go", "--json")
	if code != 1 || !strings.Contains(stdout, `"outcome":"error"`) || !strings.Contains(stdout, "upstream exploded") {
		t.Fatalf("exit %d stdout %q", code, stdout)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"-p", "x", "--effort", "extreme"}, {"  "}} {
		if _, stderr, code := gilda(t, "", args...); code != 2 || !strings.HasPrefix(stderr, "gilda:") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}

func TestPositionalPromptWithDashPIsRefused(t *testing.T) {
	_, stderr, code := gilda(t, script, "-p", "go", "also", "this")
	if code != 2 || !strings.Contains(stderr, "not both") {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

// A positional prompt starts the REPL, so flags before and after it still parse and, with no
// terminal here, the run stops at the REPL's terminal check.
func TestPositionalPromptStartsTheREPL(t *testing.T) {
	for _, args := range [][]string{{"-m", "x", "do", "it"}, {"do it", "--permissions", "auto"}, {}} {
		_, stderr, code := gilda(t, script, args...)
		if code != 2 || !strings.Contains(stderr, "the REPL needs a terminal") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}

func TestPromptFromStdin(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.json"), []byte(`[{"text":"ok"}]`), 0o644)
	cmd := exec.Command(bin, "--mock", "m.json", "-p", "-", "--json")
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	cmd.Stdin = strings.NewReader("from stdin")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), `"outcome":"complete"`) {
		t.Fatalf("%v %s", err, out)
	}
}

// An explicit -p is headless even when stdin turns out empty.
func TestEmptyPromptFromStdinIsAnError(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(bin, "--mock", "m.json", "-p", "-", "--json")
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), `"outcome":"error"`) {
		t.Fatalf("%v %s", err, out)
	}
}

// --json has no one to ask, so auto refuses a write outside the root, and read-only refuses
// every write; the model gets the reason and the run completes.
func TestPermissionsInJSONMode(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "escape.txt")
	script := `[{"calls":[{"name":"write","arguments":{"path":"` + outside + `","content":"x"}},
	                      {"name":"write","arguments":{"path":"inside.txt","content":"x"}}]},
	            {"text":"ok"}]`
	for mode, wantInside := range map[string]bool{"auto": true, "read-only": false} {
		stdout, _, code := gilda(t, script, "-p", "go", "--json", "--permissions", mode)
		if code != 0 {
			t.Fatalf("%s: exit %d", mode, code)
		}
		if _, err := os.Stat(outside); err == nil {
			t.Fatalf("%s: wrote outside the root", mode)
		}
		refusals := strings.Count(stdout, `"error":"refused:`)
		if want := map[bool]int{true: 1, false: 2}[wantInside]; refusals != want {
			t.Fatalf("%s: %d refusals, want %d\n%s", mode, refusals, want, stdout)
		}
		if !strings.Contains(stdout, `"permissions":"`+mode+`"`) {
			t.Fatalf("%s: result lacks the mode", mode)
		}
	}
	if _, stderr, code := gilda(t, "", "-p", "x", "--permissions", "yolo"); code != 2 || !strings.Contains(stderr, "permissions") {
		t.Fatalf("bad mode: %d %q", code, stderr)
	}
}

func TestHelpFitsEightyColumns(t *testing.T) {
	stdout, _, code := gilda(t, "", "--help")
	if code != 0 || !strings.Contains(stdout, "--permissions MODE") {
		t.Fatalf("exit %d:\n%s", code, stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) > 80 {
			t.Errorf("%d columns: %q", len(line), line)
		}
	}
}

// GILDA_API_KEY must not reach --help, as a flag default would; --api-key warns that it leaks.
func TestAPIKeyStaysOutOfHelp(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(bin, "--help")
	cmd.Env = testEnv("GILDA_API_KEY=sk-secret", "XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "sk-secret") {
		t.Fatalf("err %v, help shows the key:\n%s", err, out)
	}
	if _, stderr, _ := gilda(t, "", "--api-key", "k", "-p", "x"); !strings.Contains(stderr, "warning: --api-key") {
		t.Errorf("no warning for --api-key: %q", stderr)
	}
}

// settings.toml adds protections; it cannot lift a built-in one, and a bad file stops the run.
func TestSettingsAddProtections(t *testing.T) {
	script := `[{"calls":[{"name":"write","arguments":{"path":".git/x","content":"1"}},
	                      {"name":"write","arguments":{"path":"key.pem","content":"2"}},
	                      {"name":"write","arguments":{"path":"ok.txt","content":"3"}}]},
	            {"text":"ok"}]`
	settings := "[permissions]\nsecrets = [\"*.pem\"]\nprotected = [\"!.git\"]\n"
	stdout, _ := gildaWithSettings(t, settings, script, 0, "-p", "go", "--json")
	for _, want := range []string{
		"refused: auto mode asks before write under .git",
		"refused: auto mode asks before write of key.pem, which may hold secrets",
		`"label":"write ok.txt","name":"write","ok":true`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing %q in\n%s", want, stdout)
		}
	}
	_, stderr := gildaWithSettings(t, "[permissions]\nsecret = []\n", script, 1, "-p", "go")
	if !strings.Contains(stderr, "unknown keys: permissions.secret") {
		t.Errorf("bad settings: %q", stderr)
	}
}

func gildaWithSettings(t *testing.T, settings, script string, wantCode int, args ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cfg", "gilda"), 0o700)
	os.WriteFile(filepath.Join(dir, "cfg", "gilda", "settings.toml"), []byte(settings), 0o600)
	os.WriteFile(filepath.Join(dir, "mock.json"), []byte(script), 0o600)
	cmd := exec.Command(bin, append([]string{"--mock", "mock.json"}, args...)...)
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	if code != wantCode {
		t.Fatalf("exit %d, want %d: %s", code, wantCode, errb.String())
	}
	return out.String(), errb.String()
}

// The settings file's mode applies unless --permissions names one.
func TestSettingsMode(t *testing.T) {
	script := `[{"calls":[{"name":"write","arguments":{"path":"ok.txt","content":"1"}}]},{"text":"ok"}]`
	settings := "[permissions]\nmode = \"read-only\"\n"
	stdout, _ := gildaWithSettings(t, settings, script, 0, "-p", "go", "--json")
	if !strings.Contains(stdout, `"permissions":"read-only"`) || !strings.Contains(stdout, "refused: write") {
		t.Fatalf("settings mode not applied:\n%s", stdout)
	}
	stdout, _ = gildaWithSettings(t, settings, script, 0, "-p", "go", "--json", "--permissions", "auto")
	if !strings.Contains(stdout, `"permissions":"auto"`) || strings.Contains(stdout, "refused") {
		t.Fatalf("flag did not win:\n%s", stdout)
	}
}

// In ask mode an allowlisted command runs; any other, or a chained one, is refused when no one
// can be asked.
func TestCommandAllowlist(t *testing.T) {
	script := `[{"calls":[{"name":"bash","arguments":{"command":"echo hi"}},
	                      {"name":"bash","arguments":{"command":"echo hi; touch x"}},
	                      {"name":"bash","arguments":{"command":"touch y"}}]},
	            {"text":"ok"}]`
	settings := "[permissions]\nmode = \"ask\"\ncommands = [\"echo\"]\n"
	stdout, _ := gildaWithSettings(t, settings, script, 0, "-p", "go", "--json")
	if !strings.Contains(stdout, `"label":"$ echo hi","name":"bash","ok":true`) {
		t.Errorf("allowlisted command did not run:\n%s", stdout)
	}
	if strings.Count(stdout, "refused: ask mode asks before bash") != 2 {
		t.Errorf("want 2 refusals:\n%s", stdout)
	}
}

// A retried request shows as a retry record in --json and a [retry] line on -p's stderr.
func TestRetriesAreShown(t *testing.T) {
	answer := llmtest.SSE("", `{"id":"g","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`, "", "[DONE]")
	for _, asJSON := range []bool{true, false} {
		srv := llmtest.New(t, llmtest.Status(200, `{"data":[]}`), llmtest.Status(502, `{"error":{"message":"upstream"}}`), answer)
		dir := t.TempDir()
		args := []string{"-P", "openrouter", "-m", "x/y", "--base-url", srv.URL, "-p", "go"}
		if asJSON {
			args = append(args, "--json")
		}
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir, "OPENROUTER_API_KEY=k")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %s", err, errb.String())
		}
		if asJSON && !strings.Contains(out.String(), `{"attempt":1,"reason":"502 Bad Gateway","type":"retry"}`) {
			t.Errorf("no retry record:\n%s", out.String())
		}
		if !asJSON && !strings.Contains(errb.String(), "[retry] 1 after 502 Bad Gateway") {
			t.Errorf("no retry line:\n%s", errb.String())
		}
	}
}

func TestTheUsersGildaVariablesDoNotReachTheBinary(t *testing.T) {
	t.Setenv("GILDA_PERMISSIONS", "read-only")
	t.Setenv("GILDA_LOG", filepath.Join(t.TempDir(), "log"))
	stdout, _, code := gilda(t, `[{"calls":[{"name":"write","arguments":{"path":"x","content":"1"}}]},{"text":"ok"}]`, "-p", "go", "--json")
	if code != 0 || !strings.Contains(stdout, `"permissions":"auto"`) {
		t.Fatalf("exit %d, the user's GILDA_PERMISSIONS leaked:\n%s", code, stdout)
	}
	if _, err := os.Stat(os.Getenv("GILDA_LOG")); err == nil {
		t.Fatal("the user's GILDA_LOG leaked")
	}
}

// An answer cut at the output limit exits 0, like a whole one, but says so.
func TestTruncatedAnswer(t *testing.T) {
	const cut = `[{"text": "half an ans", "stop": "max_tokens"}]`
	stdout, _, code := gilda(t, cut, "-p", "go", "--json")
	if code != 0 || !strings.Contains(stdout, `"outcome":"truncated"`) || !strings.Contains(stdout, `"error":null`) {
		t.Fatalf("exit %d stdout %q", code, stdout)
	}
	stdout, stderr, code := gilda(t, cut, "-p", "go")
	if code != 0 || stdout != "half an ans\n" || !strings.Contains(stderr, "cut off at the output limit") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// A stream cut off partway is sent again; stdout holds only the answer that finished.
func TestResentAnswerPrintsOnce(t *testing.T) {
	stdout, stderr, code := gilda(t, `[{"text": "The ans", "incomplete": true}, {"text": "The answer."}]`, "-p", "go")
	if code != 0 || stdout != "The answer.\n" || !strings.Contains(stderr, "[retry] 1") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// With no one to answer the trust question, a run under an AGENTS.md uses ask mode, and records
// nothing.
func TestUntrustedCheckoutUsesAsk(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "m.json"), []byte(`[{"text":"ok"}]`), 0o644)
	for mode, want := range map[string]string{"": `"permissions":"ask"`, "auto": `"permissions":"auto"`} {
		args := []string{"--mock", "m.json", "-p", "go", "--json"}
		if mode != "" {
			args = append(args, "--permissions", mode)
		}
		cmd := exec.Command(bin, args...)
		cmd.Dir = dir
		cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), want) {
			t.Errorf("mode %q: %v %s", mode, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gilda", "state.json")); err == nil {
		data, _ := os.ReadFile(filepath.Join(dir, "gilda", "state.json"))
		if strings.Contains(string(data), "trust") {
			t.Errorf("an unanswered question was recorded: %s", data)
		}
	}
}

// A -p run saves its session; -c resumes it, and --sessions lists it.
func TestSessionsResume(t *testing.T) {
	dir := t.TempDir()
	answer := `[{"text": "first answer"}]`
	stdout, stderr, code := gildaIn(t, dir, answer, "-p", "one", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		_ = json.Unmarshal([]byte(line), &last)
	}
	id, _ := last["session_id"].(string)
	if id == "" {
		t.Fatalf("result has no session_id: %v", last)
	}
	if _, stderr, code := gildaIn(t, dir, `[{"text": "second answer"}]`, "-c", "-p", "two"); code != 0 {
		t.Fatalf("continue: exit %d: %s", code, stderr)
	}
	stdout, _, code = gildaIn(t, dir, "", "--sessions")
	if code != 0 || !strings.Contains(stdout, id) || !strings.Contains(stdout, "4 msgs") || !strings.Contains(stdout, "  one") {
		t.Fatalf("sessions: %q", stdout)
	}
	if _, stderr, code := gildaIn(t, dir, answer, "--resume", "nope", "-p", "x"); code == 0 || !strings.Contains(stderr, "no saved session") {
		t.Fatalf("unknown id: exit %d %q", code, stderr)
	}
}

// --acp takes prompts and sessions from the client, so flags that give them are refused.
func TestACPRefusesPromptFlags(t *testing.T) {
	for _, args := range [][]string{{"--acp", "-p", "x"}, {"--acp", "hello"}, {"--acp", "--json"}, {"--acp", "-c"}} {
		if _, stderr, code := gilda(t, "", args...); code != 2 || !strings.Contains(stderr, "--acp") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}

// gilda --acp answers a prompt over stdin and stdout, and stdout carries only JSON-RPC.
func TestACPAnswersAPrompt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mock.json"), []byte(script), 0o644)
	cmd := exec.Command(bin, "--acp", "--mock", "mock.json", "--permissions", "auto")
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	// next returns the response to id, failing on any line that is not JSON-RPC 2.0.
	next := func(id int) map[string]any {
		for lines.Scan() {
			var m map[string]any
			if err := json.Unmarshal(lines.Bytes(), &m); err != nil || m["jsonrpc"] != "2.0" {
				t.Fatalf("stdout line is not JSON-RPC: %q", lines.Text())
			}
			if m["id"] == float64(id) {
				return m
			}
		}
		t.Fatalf("stdout ended before response %d; stderr %s", id, stderr.String())
		return nil
	}
	send := func(id int, method string, params any) {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(data, '\n'))
	}
	send(1, "initialize", map[string]any{"protocolVersion": 1})
	next(1)
	send(2, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}})
	session := next(2)["result"].(map[string]any)["sessionId"]
	send(3, "session/prompt", map[string]any{"sessionId": session, "prompt": []map[string]string{{"type": "text", "text": "go"}}})
	if r := next(3)["result"]; r == nil || r.(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("prompt: %v; stderr %s", r, stderr.String())
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit: %v; stderr %s", err, stderr.String())
	}
}

// startACP runs gilda --acp in dir with args, and returns a client for it.
func startACP(t *testing.T, dir string, args ...string) (*exec.Cmd, func(int, string, any) map[string]any, *bytes.Buffer) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "mock.json"), []byte(script), 0o644)
	cmd := exec.Command(bin, append([]string{"--acp", "--mock", filepath.Join(dir, "mock.json"), "--permissions", "auto"}, args...)...)
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	lines := bufio.NewScanner(stdout)
	call := func(id int, method string, params any) map[string]any {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(data, '\n'))
		for lines.Scan() {
			var m map[string]any
			if json.Unmarshal(lines.Bytes(), &m) == nil && m["id"] == float64(id) {
				return m
			}
		}
		t.Fatalf("stdout ended before response %d; stderr %s", id, stderr.String())
		return nil
	}
	return cmd, call, stderr
}

// errCode is the error code of a response, or 0.
func errCode(m map[string]any) float64 {
	if e, ok := m["error"].(map[string]any); ok {
		return e["code"].(float64)
	}
	return 0
}

// A session cwd must be within --root, by default the directory gilda started in.
func TestACPBoundsTheSessionCwd(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	_, call, _ := startACP(t, dir)
	call(1, "initialize", map[string]any{"protocolVersion": 1})
	if m := call(2, "session/new", map[string]any{"cwd": other, "mcpServers": []any{}}); errCode(m) != -32602 {
		t.Errorf("a cwd outside the start directory: %v", m)
	}
	if m := call(3, "session/new", map[string]any{"cwd": sub, "mcpServers": []any{}}); m["result"] == nil {
		t.Errorf("a cwd inside the start directory: %v", m)
	}
	_, call, _ = startACP(t, dir, "--root", sub)
	call(1, "initialize", map[string]any{"protocolVersion": 1})
	if m := call(2, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}); errCode(m) != -32602 {
		t.Errorf("a cwd above --root: %v", m)
	}
	_, call, _ = startACP(t, dir, "--root", "/")
	call(1, "initialize", map[string]any{"protocolVersion": 1})
	if m := call(2, "session/new", map[string]any{"cwd": other, "mcpServers": []any{}}); m["result"] == nil {
		t.Errorf("a cwd under --root /: %v", m)
	}
}

// SIGINT ends gilda --acp cleanly, as SIGTERM does.
func TestACPExitsCleanlyOnSIGINT(t *testing.T) {
	cmd, call, stderr := startACP(t, t.TempDir())
	call(1, "initialize", map[string]any{"protocolVersion": 1})
	cmd.Process.Signal(os.Interrupt)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit after SIGINT: %v; stderr %s", err, stderr.String())
	}
}

// A client that stops reading mid-turn closes gilda's stdout. gilda must not die of SIGPIPE,
// and must not run on for nobody: it saves the session and kills the turn's background job.
func TestACPSavesAndCleansUpWhenTheClientDies(t *testing.T) {
	dir := t.TempDir()
	mock := `[{"text": "start", "calls": [{"name": "bash", "arguments": {"command": "(echo $$ > job.pid; exec sleep 60) > /dev/null 2>&1 & sleep 0.2; echo bg"}}]},
 {"text": "wait", "calls": [{"name": "bash", "arguments": {"command": "sleep 2"}}]},
 {"text": "Done."}]`
	os.WriteFile(filepath.Join(dir, "mock.json"), []byte(mock), 0o644)
	cmd := exec.Command(bin, "--acp", "--mock", filepath.Join(dir, "mock.json"), "--permissions", "auto")
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	send := func(id int, method string, params any) {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(data, '\n'))
	}
	lines := bufio.NewScanner(stdout)
	until := func(match func(map[string]any) bool) map[string]any {
		for lines.Scan() {
			var m map[string]any
			if json.Unmarshal(lines.Bytes(), &m) == nil && match(m) {
				return m
			}
		}
		t.Fatalf("stdout ended; stderr %s", stderr.String())
		return nil
	}
	send(1, "initialize", map[string]any{"protocolVersion": 1})
	until(func(m map[string]any) bool { return m["id"] == float64(1) })
	send(2, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}})
	sid := until(func(m map[string]any) bool { return m["id"] == float64(2) })["result"].(map[string]any)["sessionId"]
	send(3, "session/prompt", map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": "go"}}})
	// The second call is running once its tool_call is out.
	until(func(m map[string]any) bool { return strings.Contains(fmt.Sprint(m["params"]), "sleep 2") })
	// stdin stays open: the write after sleep 2 fails, and that alone must stop gilda.
	stdout.Close()
	defer stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) && !exit.Exited() {
			t.Fatalf("gilda died of %v; stderr %s", exit, stderr.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("gilda still ran 10 s after its client died")
	}
	pid, err := os.ReadFile(filepath.Join(dir, "job.pid"))
	if err != nil {
		t.Fatalf("the background job never started: %v", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(pid)))
	if n > 0 && syscall.Kill(n, 0) == nil {
		syscall.Kill(n, syscall.SIGKILL)
		t.Error("the turn's background job outlived gilda")
	}
	saved, _ := filepath.Glob(filepath.Join(dir, "gilda", "sessions", "*", "*"))
	if len(saved) == 0 {
		saved, _ = filepath.Glob(filepath.Join(dir, "gilda", "sessions", "*"))
	}
	if len(saved) == 0 {
		t.Error("no session was saved")
	}
}

// acpProcess starts gilda --acp in dir on mock, initializes it and opens a session.
func acpProcess(t *testing.T, dir, mock string) (*exec.Cmd, func(int, string, any), *bufio.Scanner, any, *bytes.Buffer) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "mock.json"), []byte(mock), 0o644)
	cmd := exec.Command(bin, "--acp", "--mock", filepath.Join(dir, "mock.json"), "--permissions", "auto")
	cmd.Dir = dir
	cmd.Env = testEnv("XDG_STATE_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	send := func(id int, method string, params any) {
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(data, '\n'))
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 1<<24)
	reply := func(id int) map[string]any {
		for lines.Scan() {
			var m map[string]any
			if json.Unmarshal(lines.Bytes(), &m) == nil && m["id"] == float64(id) {
				return m
			}
		}
		t.Fatalf("stdout ended before response %d; stderr %s", id, stderr.String())
		return nil
	}
	send(1, "initialize", map[string]any{"protocolVersion": 1})
	reply(1)
	send(2, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}})
	sid := reply(2)["result"].(map[string]any)["sessionId"]
	return cmd, send, lines, sid, stderr
}

// exited waits up to d for cmd to end.
func exited(cmd *exec.Cmd, d time.Duration) (error, bool) {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err, true
	case <-time.After(d):
		return nil, false
	}
}

// A client that stops reading blocks gilda's writes, and so its shutdown. A second signal
// still ends it.
func TestACPASecondSignalEndsAStuckShutdown(t *testing.T) {
	dir := t.TempDir()
	cmd, send, _, sid, stderr := acpProcess(t, dir, `[{"text": "`+strings.Repeat("word ", 100000)+`"}]`)
	send(3, "session/prompt", map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": "go"}}})
	time.Sleep(time.Second) // stdout is not read, so the pipe fills
	cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(500 * time.Millisecond)
	cmd.Process.Signal(syscall.SIGTERM)
	if _, ok := exited(cmd, 5*time.Second); !ok {
		t.Fatalf("gilda still ran 5 s after two SIGTERMs; stderr %s", stderr.String())
	}
}

// One signal ends a running prompt cleanly and saves the session.
func TestACPSavesTheSessionOnASignal(t *testing.T) {
	dir := t.TempDir()
	cmd, send, lines, sid, stderr := acpProcess(t, dir,
		`[{"text": "wait", "calls": [{"name": "bash", "arguments": {"command": "sleep 5"}}]}, {"text": "Done."}]`)
	send(3, "session/prompt", map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": "go"}}})
	for lines.Scan() && !strings.Contains(lines.Text(), "sleep 5") {
	}
	go func() {
		for lines.Scan() {
		}
	}()
	cmd.Process.Signal(syscall.SIGTERM)
	err, ok := exited(cmd, 5*time.Second)
	if !ok || err != nil {
		t.Fatalf("exit %v (ended %v); stderr %s", err, ok, stderr.String())
	}
	if saved, _ := filepath.Glob(filepath.Join(dir, "gilda", "sessions", "*.json")); len(saved) == 0 {
		t.Error("no session was saved")
	}
}

// A background task's answer comes back through task_collect; --json reports the task, and
// text mode tags its line on stderr.
func TestHeadlessBackgroundTask(t *testing.T) {
	// One response starts the task and waits for it, so the task takes the second step.
	script := `[
  {"calls": [{"name": "task", "arguments": {"prompt": "look", "background": true, "name": "look"}},
             {"name": "task_collect", "arguments": {"wait": 10}}]},
  {"text": "child answer", "usage": {"input_tokens": 7, "output_tokens": 1}},
  {"text": "final", "usage": {"input_tokens": 20, "output_tokens": 3}}
]`
	stdout, stderr, code := gilda(t, script, "-p", "go", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var collected, task bool
	var last map[string]any
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("not JSON: %q", sc.Text())
		}
		switch {
		case rec["type"] == "tool_result" && rec["name"] == "task_collect":
			collected = strings.Contains(rec["output"].(string), "child answer")
		case rec["type"] == "task":
			task = rec["id"] == "t1" && rec["background"] == true
		}
		last = rec
	}
	if !collected || !task || last["text"] != "final" {
		t.Fatalf("collected %v task %v result %v\n%s", collected, task, last, stdout)
	}
	if u := last["usage"].(map[string]any); u["input_tokens"] != float64(27) {
		t.Fatalf("usage %v leaves out the task", u)
	}

	_, stderr, code = gilda(t, script, "-p", "go")
	if code != 0 || !strings.Contains(stderr, `[t1] "look" finished, 1 turn`) {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}

func TestTaskModelFlagReachesTheStartRecord(t *testing.T) {
	stdout, stderr, code := gilda(t, `[{"text":"ok"}]`, "-p", "go", "--json", "--task-model", "small")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var start map[string]any
	json.Unmarshal([]byte(strings.SplitN(stdout, "\n", 2)[0]), &start)
	if start["type"] != "start" || start["task_model"] != "small" {
		t.Fatalf("start record %v", start)
	}
}
