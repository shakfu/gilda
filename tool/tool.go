// Package tool holds the agent's tools. The default set is four: read, write, edit and bash.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"unicode/utf8"

	"github.com/shakfu/gilda/llm"
)

// OutputCap is the default bound on one tool result in bytes, about 8k tokens. Over the cap the first fifth and
// the last four fifths are kept, since errors and summaries come last.
const OutputCap = 32 << 10

type Result struct {
	// Output is what the model sees.
	Output string
	// Summary is one line for the user, such as "400 lines" or "exit 1".
	Summary string
	// Failed marks work that ran but reported a problem, such as a non-zero exit. The model
	// gets the output either way; the user sees the call in a warning colour.
	Failed bool
}

type Tool interface {
	Spec() llm.ToolSpec
	// Label names the call for the user, such as "read main.go:1-80" or "$ go test".
	Label(args json.RawMessage) string
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// ReadOnly is implemented by a tool that touches nothing outside gilda's process except to
// read local files: it writes no file, starts no process and makes no network request.
// Permission modes run such a tool without asking, even in read-only mode, so the claim must
// hold for every call. A network tool must not declare it, even one that only fetches: a
// request can carry out whatever the model has read. A tool that does not implement ReadOnly
// is taken to modify the environment.
type ReadOnly interface {
	ReadOnly() bool
}

// Previewer is implemented by a tool that can show, before a call runs, what it would change,
// such as a diff. Preview must change nothing.
type Previewer interface {
	Preview(args json.RawMessage) (string, error)
}

// Binder is implemented by a tool that resolves what a call touches before it is approved.
// The agent approves and runs the Tool that Bind returns in place of the receiver, so the
// paths approval checks, the preview the user sees and the files Run changes are the same. A
// symlink swapped in between is not followed; write and edit fail if the file or a directory
// above it was replaced. A Bind error is returned only after approval, since it can reveal
// what an unapproved file holds.
type Binder interface {
	Bind(args json.RawMessage) (Tool, error)
}

// Hosts is implemented by a network tool: it names the hosts a call contacts, such as
// "pkg.go.dev". A network tool is never read-only, whatever ReadOnly says. Permission modes
// run a call to hosts in the allowlist without asking; any other host asks, or is refused in
// read-only mode. A network tool should also implement Paths, returning no paths when it
// writes no files; otherwise it is taken to write anywhere. An error means the arguments are
// invalid and the tool will reject them. HostOf extracts a host from a URL.
type Hosts interface {
	Hosts(args json.RawMessage) ([]string, error)
}

// HostOf returns the host of an absolute URL, without its port.
func HostOf(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("%q has no host", rawURL)
	}
	return u.Hostname(), nil
}

// Paths is implemented by a tool whose calls touch files named in their arguments. For a
// tool that modifies the environment, auto mode runs a call without asking only when every
// path is inside the working directory and none is under .git or a secret such as .env. A
// read-only tool's paths are checked for secrets. An error means the arguments are invalid
// and the tool will reject them.
type Paths interface {
	Paths(args json.RawMessage) ([]string, error)
}

// Env is what every tool shares.
type Env struct {
	// Root resolves relative paths and is the working directory for bash.
	Root string
	// Jobs tracks background processes left by bash, killed when the agent exits.
	Jobs *Jobs
	// Limits bound the built-in tools; a zero field takes its value in DefaultLimits.
	Limits Limits
	// Hide names environment variables bash does not pass on, such as provider keys.
	Hide []string
}

// Limits bound what the built-in tools return, read and run. OutputCap, ReadLines and
// ReadLineBytes bound what reaches the model, so they set the tokens a call can cost.
type Limits struct {
	// OutputCap bounds one result in bytes; agent.Config.OutputCap should match it.
	OutputCap int
	// ReadLines bounds the lines one read returns; ReadLineBytes bounds one line.
	ReadLines, ReadLineBytes int
	// BashTimeout is the default seconds before a command is killed; BashMaxTimeout bounds
	// what a call may ask for.
	BashTimeout, BashMaxTimeout int
	// DiffBytes bounds the existing file a write preview reads.
	DiffBytes int
}

// DefaultLimits are the limits a zero field takes.
var DefaultLimits = Limits{
	OutputCap: OutputCap, ReadLines: 2000, ReadLineBytes: 2000,
	BashTimeout: 120, BashMaxTimeout: 600, DiffBytes: 1 << 20,
}

// limits returns e.Limits with each zero field set to its default.
func (e Env) limits() Limits {
	l, d := e.Limits, DefaultLimits
	for _, f := range []struct {
		v *int
		d int
	}{
		{&l.OutputCap, d.OutputCap}, {&l.ReadLines, d.ReadLines}, {&l.ReadLineBytes, d.ReadLineBytes},
		{&l.BashTimeout, d.BashTimeout}, {&l.BashMaxTimeout, d.BashMaxTimeout}, {&l.DiffBytes, d.DiffBytes},
	} {
		if *f.v == 0 {
			*f.v = f.d
		}
	}
	return l
}

// Default returns read, write, edit and bash.
func Default(env Env) []Tool {
	return []Tool{Read{env}, Write{env}, Edit{env}, Bash{env}}
}

// Specs lists the tools' definitions in order.
func Specs(tools []Tool) []llm.ToolSpec {
	out := make([]llm.ToolSpec, len(tools))
	for i, t := range tools {
		out[i] = t.Spec()
	}
	return out
}

// Find returns the tool with the given name.
func Find(tools []Tool, name string) Tool {
	for _, t := range tools {
		if t.Spec().Name == name {
			return t
		}
	}
	return nil
}

func (e Env) abs(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(e.Root, path)
}

// decode parses arguments into v. Empty arguments mean an empty object. A required key must be
// present and not null: decoded into a string, either would pass as "", and write would empty
// the file.
func decode(args json.RawMessage, v any, required ...string) error {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(args, &keys)
	for _, k := range required {
		if raw, ok := keys[k]; !ok || string(raw) == "null" {
			return fmt.Errorf("%s is required", k)
		}
	}
	return nil
}

// Cap keeps the head and tail of s within limit bytes, marking what was cut, and repairs
// invalid UTF-8.
func Cap(s string, limit int) string {
	if !utf8.ValidString(s) {
		s = string([]rune(s))
	}
	if len(s) <= limit {
		return s
	}
	head := limit / 5
	tail := limit - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return fmt.Sprintf("%s\n[... %d bytes omitted ...]\n%s", s[:head], start-head, s[start:])
}

func schema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// pathArg returns the "path" argument shared by read, write and edit.
func pathArg(args json.RawMessage) ([]string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	return []string{a.Path}, nil
}

// plural formats a count with its noun: "1 line", "3 lines".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (Read) ReadOnly() bool                                { return true }
func (Read) Paths(args json.RawMessage) ([]string, error)  { return pathArg(args) }
func (Write) Paths(args json.RawMessage) ([]string, error) { return pathArg(args) }
func (Edit) Paths(args json.RawMessage) ([]string, error)  { return pathArg(args) }
