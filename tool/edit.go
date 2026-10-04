package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/shakfu/gilda/llm"
)

type Edit struct{ Env }

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (Edit) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "edit",
		Description: "Replace exact text in a file. old_string must occur exactly once unless replace_all is true.",
		Schema: schema([]string{"path", "old_string", "new_string"}, map[string]any{
			"path":        prop("string", "File path, absolute or relative to the working directory."),
			"old_string":  prop("string", "Exact text to replace, without read's line-number prefixes."),
			"new_string":  prop("string", "Replacement text."),
			"replace_all": prop("boolean", "Replace every occurrence."),
		}),
	}
}

func (Edit) Label(raw json.RawMessage) string {
	var a editArgs
	_ = decode(raw, &a)
	return "edit " + a.Path
}

func (e Edit) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	b, err := e.Bind(raw)
	if err != nil {
		return Result{}, err
	}
	return b.Run(ctx, raw)
}

// Preview returns the unified diff Run would apply, computed the same way.
func (e Edit) Preview(raw json.RawMessage) (string, error) {
	b, err := e.Bind(raw)
	if err != nil {
		return "", err
	}
	return b.(Previewer).Preview(raw)
}

// Bind reads the file and works out the edit; the bound call applies it only if the file is
// unchanged. See Binder.
func (e Edit) Bind(raw json.RawMessage) (Tool, error) {
	c, err := e.apply(raw)
	if err != nil {
		return nil, err
	}
	return boundEdit{e, c}, nil
}

// boundEdit is an edit fixed to its file and its result. It ignores the arguments its methods
// are passed.
type boundEdit struct {
	Edit
	change
}

func (b boundEdit) Paths(json.RawMessage) ([]string, error) { return b.target.paths(), nil }

func (b boundEdit) Run(context.Context, json.RawMessage) (Result, error) {
	if err := b.target.write([]byte(b.after), []byte(b.before)); err != nil {
		return Result{}, err
	}
	b.Seen.recordPath(b.target.path)
	summary := "1 replacement"
	if b.n > 1 {
		summary = fmt.Sprintf("%d replacements", b.n)
	}
	return Result{Output: fmt.Sprintf("edited %s: %s", b.target.name, summary), Summary: summary}, nil
}

func (b boundEdit) Preview(json.RawMessage) (string, error) {
	name := b.target.name
	return udiff.Unified(name, name, b.before, b.after), nil
}

type change struct {
	target        target
	before, after string
	n             int
}

// apply works out an edit without writing it. It refuses an ambiguous match: an edit that
// hits the wrong occurrence is worse than a failed one.
func (e Edit) apply(raw json.RawMessage) (change, error) {
	var a editArgs
	if err := decode(raw, &a, "path", "old_string", "new_string"); err != nil {
		return change{}, err
	}
	if a.Path == "" {
		return change{}, fmt.Errorf("path is required")
	}
	// An empty pattern matches between every character.
	if a.OldString == "" {
		return change{}, fmt.Errorf("old_string is empty; use write to create a file")
	}
	if a.OldString == a.NewString {
		return change{}, fmt.Errorf("old_string and new_string are identical")
	}
	t, err := bind(e.abs(a.Path), a.Path)
	if err != nil {
		return change{}, err
	}
	f, info, err := openRegular(t.path, a.Path)
	if err != nil {
		return change{}, err
	}
	data, err := readForEdit(f, info, a.Path)
	f.Close()
	if err != nil {
		return change{}, err
	}
	if t.file == nil || !t.same(info) {
		return change{}, t.changed()
	}
	if err := e.Seen.check(t.path, info, a.Path, "editing"); err != nil {
		return change{}, err
	}
	text := string(data)
	old, repl := a.OldString, a.NewString
	// read shows lines without their \r, so a multi-line old_string copied from it cannot match
	// a CRLF file as given.
	if strings.Contains(old, "\n") && !strings.Contains(text, old) && strings.Contains(text, "\r\n") {
		old = strings.ReplaceAll(old, "\n", "\r\n")
		repl = strings.ReplaceAll(repl, "\n", "\r\n")
	}
	n := strings.Count(text, old)
	switch {
	case n == 0:
		return change{}, fmt.Errorf("old_string not found in %s", a.Path)
	case n > 1 && !a.ReplaceAll:
		return change{}, fmt.Errorf("old_string occurs %d times in %s; add context or set replace_all", n, a.Path)
	}
	c := change{target: t, before: text, n: n}
	if a.ReplaceAll {
		c.after = strings.ReplaceAll(text, old, repl)
	} else {
		c.after = strings.Replace(text, old, repl, 1)
	}
	return c, nil
}
