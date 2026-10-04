package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	for _, args := range [][]string{{"--json"}, {"stray"}, {"-p", "x", "--effort", "extreme"}} {
		if _, stderr, code := gilda(t, "", args...); code != 2 || !strings.HasPrefix(stderr, "gilda:") {
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
