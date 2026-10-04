package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aymanbagabas/go-udiff"

	"github.com/shakfu/gilda/llm"
)

type Write struct{ Env }

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (Write) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "write",
		Description: "Create or replace a file with the given content. Creates parent directories.",
		Schema: schema([]string{"path", "content"}, map[string]any{
			"path":    prop("string", "File path, absolute or relative to the working directory."),
			"content": prop("string", "The complete file content."),
		}),
	}
}

func (Write) Label(raw json.RawMessage) string {
	var a writeArgs
	_ = decode(raw, &a)
	return "write " + a.Path
}

func (w Write) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	b, err := w.Bind(raw)
	if err != nil {
		return Result{}, err
	}
	return b.Run(ctx, raw)
}

// Preview returns a unified diff against the file the write replaces; see boundWrite.Preview.
func (w Write) Preview(raw json.RawMessage) (string, error) {
	b, err := w.Bind(raw)
	if err != nil {
		return "", err
	}
	return b.(Previewer).Preview(raw)
}

// Bind fixes the call to the file its path resolves to now; see Binder.
func (w Write) Bind(raw json.RawMessage) (Tool, error) {
	a, err := parseWrite(raw)
	if err != nil {
		return nil, err
	}
	t, err := bind(w.abs(a.Path), a.Path)
	if err != nil {
		return nil, err
	}
	// A new file needs no read; replacing one the model has not seen loses its content.
	if t.file != nil {
		if err := w.Seen.check(t.path, t.file, a.Path, "replacing"); err != nil {
			return nil, err
		}
	}
	return boundWrite{w, a, t}, nil
}

func parseWrite(raw json.RawMessage) (writeArgs, error) {
	var a writeArgs
	if err := decode(raw, &a, "path", "content"); err != nil {
		return a, err
	}
	if a.Path == "" {
		return a, fmt.Errorf("path is required")
	}
	return a, nil
}

// boundWrite is a write fixed to its target. It ignores the arguments its methods are passed.
type boundWrite struct {
	Write
	args   writeArgs
	target target
}

func (b boundWrite) Paths(json.RawMessage) ([]string, error) { return b.target.paths(), nil }

func (b boundWrite) Run(context.Context, json.RawMessage) (Result, error) {
	if err := b.target.write([]byte(b.args.Content), nil); err != nil {
		return Result{}, err
	}
	b.Seen.recordPath(b.target.path)
	size := plural(len(b.args.Content), "byte")
	return Result{Output: "wrote " + size + " to " + b.args.Path, Summary: size}, nil
}

// Preview returns a unified diff against the bound file; a new file diffs against /dev/null. A
// file over Limits.DiffBytes or holding a NUL byte gets a one-line summary instead: Run never
// reads the old file, so without a bound a preview could cost more than the write.
func (b boundWrite) Preview(json.RawMessage) (string, error) {
	a, t := b.args, b.target
	if t.file == nil {
		return udiff.Unified("/dev/null", a.Path, "", a.Content), nil
	}
	f, info, err := openRegular(t.path, a.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if !t.same(info) {
		return "", t.changed()
	}
	diffCap := b.limits().DiffBytes
	data, err := io.ReadAll(io.LimitReader(f, int64(diffCap)+1))
	if err != nil {
		return "", err
	}
	if len(data) > diffCap {
		return fmt.Sprintf("replaces %s (%s) with %s; too large to diff", a.Path,
			plural(int(info.Size()), "byte"), plural(len(a.Content), "byte")), nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf("replaces binary %s (%s) with %s", a.Path,
			plural(len(data), "byte"), plural(len(a.Content), "byte")), nil
	}
	if string(data) == a.Content {
		return "content unchanged", nil
	}
	return udiff.Unified(a.Path, a.Path, string(data), a.Content), nil
}
