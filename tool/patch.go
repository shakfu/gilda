package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aymanbagabas/go-udiff"

	"github.com/shakfu/gilda/llm"
)

// Patch applies a patch in the format OpenAI's Codex models are trained to write: files added,
// updated, moved or deleted, each update as hunks of context, removed and added lines. It is
// offered to those models in place of edit's exact-string replacement, which they route around
// through bash.
type Patch struct{ Env }

const patchDescription = `Edit files with a patch. Input format:

*** Begin Patch
*** Add File: path/to/new.go
+every line of the new file, each prefixed with +
*** Update File: path/to/old.go
*** Move to: path/to/renamed.go    (optional)
@@ func Example() {                (optional: a line just above the hunk)
 context line, prefixed with a space
-removed line
+added line
*** Delete File: path/to/gone.go
*** End Patch

Give about three lines of context above and below each change. Paths are relative to the working directory. Read a file before updating or deleting it.`

type patchArgs struct {
	Input string `json:"input"`
}

func (Patch) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "apply_patch",
		Description: patchDescription,
		Schema: schema([]string{"input"}, map[string]any{
			"input": prop("string", "The whole patch, from *** Begin Patch to *** End Patch."),
		}),
	}
}

func (Patch) Label(raw json.RawMessage) string {
	ops, err := parsePatchArgs(raw)
	if err != nil {
		return "apply_patch"
	}
	var names []string
	for _, op := range ops {
		names = append(names, op.path)
	}
	return "apply_patch " + strings.Join(names, ", ")
}

// Paths names every file the patch touches, move destinations included.
func (Patch) Paths(raw json.RawMessage) ([]string, error) {
	ops, err := parsePatchArgs(raw)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, op := range ops {
		out = append(out, op.path)
		if op.moveTo != "" {
			out = append(out, op.moveTo)
		}
	}
	return out, nil
}

func (p Patch) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	b, err := p.Bind(raw)
	if err != nil {
		return Result{}, err
	}
	return b.Run(ctx, raw)
}

func (p Patch) Preview(raw json.RawMessage) (string, error) {
	b, err := p.Bind(raw)
	if err != nil {
		return "", err
	}
	return b.(Previewer).Preview(raw)
}

// Bind resolves every file and works out its new content, so nothing is written unless the
// whole patch applies. See Binder.
func (p Patch) Bind(raw json.RawMessage) (Tool, error) {
	ops, err := parsePatchArgs(raw)
	if err != nil {
		return nil, err
	}
	var changes []fileChange
	for _, op := range ops {
		c, err := p.resolve(op)
		if err != nil {
			return nil, err
		}
		changes = append(changes, c...)
	}
	return boundPatch{p, changes}, nil
}

// fileChange is one file's part of a bound patch: its new content, or its removal.
type fileChange struct {
	target        target
	before, after string
	remove        bool
	// mark is A, M, D or R, as git status shows it.
	mark string
}

func (p Patch) resolve(op patchOp) ([]fileChange, error) {
	t, err := bind(p.abs(op.path), op.path)
	if err != nil {
		return nil, err
	}
	if op.kind == opAdd {
		if t.file != nil {
			return nil, fmt.Errorf("%s exists; update it instead of adding it", op.path)
		}
		return []fileChange{{target: t, after: op.content(), mark: "A"}}, nil
	}
	if t.file == nil {
		return nil, fmt.Errorf("%s does not exist", op.path)
	}
	f, info, err := openRegular(t.path, op.path)
	if err != nil {
		return nil, err
	}
	data, err := readForEdit(f, info, op.path)
	f.Close()
	if err != nil {
		return nil, err
	}
	if !t.same(info) {
		return nil, t.changed()
	}
	verb := "updating"
	if op.kind == opDelete {
		verb = "deleting"
	}
	if err := p.Seen.check(t.path, info, op.path, verb); err != nil {
		return nil, err
	}
	before := string(data)
	if op.kind == opDelete {
		return []fileChange{{target: t, before: before, remove: true, mark: "D"}}, nil
	}
	after, err := applyHunks(before, op.hunks, op.path)
	if err != nil {
		return nil, err
	}
	if op.moveTo == "" {
		return []fileChange{{target: t, before: before, after: after, mark: "M"}}, nil
	}
	dest, err := bind(p.abs(op.moveTo), op.moveTo)
	if err != nil {
		return nil, err
	}
	if dest.file != nil {
		return nil, fmt.Errorf("cannot move %s to %s: it exists", op.path, op.moveTo)
	}
	return []fileChange{
		{target: dest, after: after, mark: "R"},
		{target: t, before: before, remove: true, mark: "D"},
	}, nil
}

// boundPatch is a patch fixed to its files and their new content. It ignores the arguments its
// methods are passed.
type boundPatch struct {
	Patch
	changes []fileChange
}

func (b boundPatch) Paths(json.RawMessage) ([]string, error) {
	var out []string
	for _, c := range b.changes {
		out = append(out, c.target.paths()...)
	}
	return out, nil
}

// Run writes the files in patch order. A file changed since Bind stops the run there; the
// error names what was already written.
func (b boundPatch) Run(context.Context, json.RawMessage) (Result, error) {
	var done []string
	for _, c := range b.changes {
		var err error
		if c.remove {
			err = c.target.remove([]byte(c.before))
		} else {
			var before []byte
			if c.target.file != nil {
				before = []byte(c.before)
			}
			if err = c.target.write([]byte(c.after), before); err == nil {
				b.Seen.recordPath(c.target.path)
			}
		}
		if err != nil {
			if len(done) > 0 {
				return Result{}, fmt.Errorf("%w; already applied: %s", err, strings.Join(done, ", "))
			}
			return Result{}, err
		}
		done = append(done, c.mark+" "+c.target.name)
	}
	summary := plural(len(done), "file")
	return Result{Output: "patched " + strings.Join(done, ", "), Summary: summary}, nil
}

func (b boundPatch) Preview(json.RawMessage) (string, error) {
	var out strings.Builder
	for _, c := range b.changes {
		from, to := c.target.name, c.target.name
		switch {
		case c.mark == "A" || c.mark == "R":
			from = "/dev/null"
		case c.remove:
			to = "/dev/null"
		}
		out.WriteString(udiff.Unified(from, to, c.before, c.after))
	}
	return out.String(), nil
}

type opKind int

const (
	opAdd opKind = iota
	opUpdate
	opDelete
)

type patchOp struct {
	kind   opKind
	path   string
	moveTo string
	added  []string // the lines of an added file
	hunks  []hunk
}

func (op patchOp) content() string {
	if len(op.added) == 0 {
		return ""
	}
	return strings.Join(op.added, "\n") + "\n"
}

// hunk replaces old with new. anchor, when set, is a line the hunk follows; eof pins it to the
// end of the file. ops holds each line's prefix, ' ', '-' or '+', in order.
type hunk struct {
	anchor   string
	old, new []string
	ops      []byte
	eof      bool
}

// replace returns the hunk's lines for file lines got, which matched old. A context line keeps
// the file's text, since it may have matched only after whitespace was ignored.
func (h hunk) replace(got []string) []string {
	var out []string
	o, n := 0, 0
	for _, op := range h.ops {
		switch op {
		case ' ':
			out = append(out, got[o])
			o, n = o+1, n+1
		case '-':
			o++
		case '+':
			out = append(out, h.new[n])
			n++
		}
	}
	return out
}

func parsePatchArgs(raw json.RawMessage) ([]patchOp, error) {
	var a patchArgs
	if err := decode(raw, &a, "input"); err != nil {
		return nil, err
	}
	return parsePatch(a.Input)
}

// parsePatch reads the patch format in patchDescription. Each file may appear once, so a patch
// cannot depend on the order its own operations apply in.
func parsePatch(text string) ([]patchOp, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, errors.New("a patch starts with *** Begin Patch")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, errors.New("a patch ends with *** End Patch")
	}
	lines = lines[1 : len(lines)-1]
	var ops []patchOp
	seen := map[string]bool{}
	for i := 0; i < len(lines); {
		line := lines[i]
		var op patchOp
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op = patchOp{kind: opAdd, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			i++
			for ; i < len(lines) && !strings.HasPrefix(lines[i], "*** "); i++ {
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("line %d: every line of an added file starts with +", i+2)
				}
				op.added = append(op.added, lines[i][1:])
			}
		case strings.HasPrefix(line, "*** Delete File: "):
			op = patchOp{kind: opDelete, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))}
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op = patchOp{kind: opUpdate, path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				i++
			}
			var err error
			if op.hunks, i, err = parseHunks(lines, i); err != nil {
				return nil, err
			}
			if len(op.hunks) == 0 && op.moveTo == "" {
				return nil, fmt.Errorf("update of %s has no changes", op.path)
			}
		case strings.TrimSpace(line) == "":
			i++
			continue
		default:
			return nil, fmt.Errorf("line %d: expected *** Add File, *** Update File or *** Delete File, got %q", i+2, line)
		}
		if op.path == "" {
			return nil, fmt.Errorf("line %d: no path", i+1)
		}
		for _, p := range []string{op.path, op.moveTo} {
			if p != "" && seen[p] {
				return nil, fmt.Errorf("%s appears twice in the patch", p)
			}
			seen[p] = p != ""
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil, errors.New("the patch changes no files")
	}
	return ops, nil
}

// parseHunks reads an update's hunks from lines[i:] up to the next file operation.
func parseHunks(lines []string, i int) ([]hunk, int, error) {
	var hunks []hunk
	var h *hunk
	for ; i < len(lines) && !isFileOp(lines[i]); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "@@"):
			hunks = append(hunks, hunk{anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@"))})
			h = &hunks[len(hunks)-1]
			continue
		case line == "*** End of File":
			if h == nil {
				return nil, i, fmt.Errorf("line %d: *** End of File outside a hunk", i+2)
			}
			h.eof = true
			continue
		}
		if h == nil {
			// The first hunk may omit its @@ line.
			hunks = append(hunks, hunk{})
			h = &hunks[0]
		}
		switch {
		case line == "":
			// An empty context line whose leading space was trimmed.
			h.old, h.new, h.ops = append(h.old, ""), append(h.new, ""), append(h.ops, ' ')
		case line[0] == ' ':
			h.old, h.new, h.ops = append(h.old, line[1:]), append(h.new, line[1:]), append(h.ops, ' ')
		case line[0] == '-':
			h.old, h.ops = append(h.old, line[1:]), append(h.ops, '-')
		case line[0] == '+':
			h.new, h.ops = append(h.new, line[1:]), append(h.ops, '+')
		default:
			return nil, i, fmt.Errorf("line %d: a hunk line starts with space, - or +, got %q", i+2, line)
		}
	}
	return hunks, i, nil
}

func isFileOp(line string) bool {
	for _, p := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: "} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// applyHunks applies hunks in order, each searched for after the one before. Lines match
// exactly, then ignoring trailing whitespace, then ignoring surrounding whitespace, since models
// often get indentation slightly wrong. The file's line endings and final newline are kept.
func applyHunks(text string, hunks []hunk, name string) (string, error) {
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	final := strings.HasSuffix(text, "\n")
	body := strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	var lines []string
	if body != "" || final {
		lines = strings.Split(body, "\n")
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	type edit struct {
		at, n int
		new   []string
	}
	var edits []edit
	pos := 0
	for k, h := range hunks {
		if h.anchor != "" {
			at := find(lines, []string{h.anchor}, pos, false)
			if at < 0 {
				return "", fmt.Errorf("%s: hunk %d: no line %q", name, k+1, h.anchor)
			}
			pos = at + 1
		}
		at := len(lines)
		if len(h.old) > 0 {
			if at = find(lines, h.old, pos, h.eof); at < 0 {
				return "", fmt.Errorf("%s: hunk %d: the lines to replace were not found; read the file again:\n%s",
					name, k+1, strings.Join(h.old, "\n"))
			}
		}
		edits = append(edits, edit{at, len(h.old), h.replace(lines[at : at+len(h.old)])})
		pos = at + len(h.old)
	}
	for k := len(edits) - 1; k >= 0; k-- {
		e := edits[k]
		lines = append(lines[:e.at], append(append([]string{}, e.new...), lines[e.at+e.n:]...)...)
	}
	out := strings.Join(lines, eol)
	// An empty file gains a final newline with its first lines; any other keeps what it had.
	if len(lines) > 0 && (final || text == "") {
		out += eol
	}
	return out, nil
}

// find returns where want first occurs in lines at or after start, or -1. With eof it tries the
// end of the file first.
func find(lines, want []string, start int, eof bool) int {
	norms := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t") },
		strings.TrimSpace,
	}
	for _, norm := range norms {
		match := func(at int) bool {
			for j, w := range want {
				if norm(lines[at+j]) != norm(w) {
					return false
				}
			}
			return true
		}
		if eof && len(lines) >= len(want) && len(lines)-len(want) >= start && match(len(lines)-len(want)) {
			return len(lines) - len(want)
		}
		for at := start; at+len(want) <= len(lines); at++ {
			if match(at) {
				return at
			}
		}
	}
	return -1
}
