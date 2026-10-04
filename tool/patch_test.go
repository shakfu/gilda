package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func patch(lines ...string) map[string]any {
	return map[string]any{"input": "*** Begin Patch\n" + strings.Join(lines, "\n") + "\n*** End Patch"}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPatchUpdatesAddsMovesAndDeletes(t *testing.T) {
	e := env(t)
	write(t, filepath.Join(e.Root, "a.go"), "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 1\n}\n")
	write(t, filepath.Join(e.Root, "old.txt"), "x\ny\n")
	write(t, filepath.Join(e.Root, "gone.txt"), "bye\n")
	p := patch(
		"*** Update File: a.go",
		"@@ func B() int {",
		"-\treturn 1",
		"+\treturn 2",
		"*** Add File: dir/new.txt",
		"+hello",
		"+world",
		"*** Update File: old.txt",
		"*** Move to: moved.txt",
		" x",
		"-y",
		"+z",
		"*** Delete File: gone.txt",
	)
	paths, err := Patch{e}.Paths(args(t, p))
	if err != nil || strings.Join(paths, " ") != "a.go dir/new.txt old.txt moved.txt gone.txt" {
		t.Fatalf("paths %v %v", paths, err)
	}
	res, err := Patch{e}.Run(context.Background(), args(t, p))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "patched M a.go, A dir/new.txt, R moved.txt, D old.txt, D gone.txt" || res.Summary != "5 files" {
		t.Fatalf("result %q %q", res.Output, res.Summary)
	}
	// The anchor puts the change in B, not in A, whose body is the same.
	if got := readFile(t, filepath.Join(e.Root, "a.go")); got != "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n" {
		t.Fatalf("a.go %q", got)
	}
	if got := readFile(t, filepath.Join(e.Root, "dir", "new.txt")); got != "hello\nworld\n" {
		t.Fatalf("new.txt %q", got)
	}
	if got := readFile(t, filepath.Join(e.Root, "moved.txt")); got != "x\nz\n" {
		t.Fatalf("moved.txt %q", got)
	}
	for _, f := range []string{"old.txt", "gone.txt"} {
		if _, err := os.Stat(filepath.Join(e.Root, f)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", f)
		}
	}
}

// Models get indentation and trailing spaces slightly wrong; a line ending and a missing final
// newline in the file must survive.
func TestPatchMatchingKeepsTheFilesShape(t *testing.T) {
	cases := []struct {
		name, before string
		hunk         []string
		after        string
	}{
		{"crlf", "a\r\nb\r\nc\r\n", []string{" a", "-b", "+B"}, "a\r\nB\r\nc\r\n"},
		{"no final newline", "a\nb", []string{"-b", "+B"}, "a\nB"},
		{"trailing spaces", "a  \nb\n", []string{" a", "-b", "+B"}, "a  \nB\n"},
		{"indentation", "\tif x {\n\t\ty()\n\t}\n", []string{"   if x {", "-    y()", "+\t\tz()"}, "\tif x {\n\t\tz()\n\t}\n"},
		{"end of file", "x\nend\nx\nend\n", []string{"-end", "+END", "*** End of File"}, "x\nend\nx\nEND\n"},
		{"insertion at the end", "a\n", []string{"+b"}, "a\nb\n"},
		{"into an empty file", "", []string{"+a"}, "a\n"},
	}
	for _, c := range cases {
		e := env(t)
		write(t, filepath.Join(e.Root, "f"), c.before)
		_, err := Patch{e}.Run(context.Background(), args(t, patch(append([]string{"*** Update File: f", "@@"}, c.hunk...)...)))
		if got := readFile(t, filepath.Join(e.Root, "f")); err != nil || got != c.after {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
}

// Nothing is written unless every file applies.
func TestAPatchThatFailsAnywhereWritesNothing(t *testing.T) {
	e := env(t)
	write(t, filepath.Join(e.Root, "a"), "1\n")
	write(t, filepath.Join(e.Root, "b"), "2\n")
	_, err := Patch{e}.Run(context.Background(), args(t, patch(
		"*** Update File: a", "-1", "+one",
		"*** Update File: b", "-3", "+three",
	)))
	if err == nil || !strings.Contains(err.Error(), "b: hunk 1: the lines to replace were not found") {
		t.Fatalf("err %v", err)
	}
	if readFile(t, filepath.Join(e.Root, "a")) != "1\n" {
		t.Fatal("a was written")
	}
}

func TestPatchRefusals(t *testing.T) {
	e := env(t)
	write(t, filepath.Join(e.Root, "a"), "1\n")
	cases := map[string]string{
		"no begin":          "*** Update File: a\n-1\n+2\n*** End Patch",
		"no end":            "*** Begin Patch\n*** Update File: a\n-1\n+2",
		"unknown op":        "*** Begin Patch\n*** Rename File: a\n*** End Patch",
		"bad hunk line":     "*** Begin Patch\n*** Update File: a\n~1\n*** End Patch",
		"add without +":     "*** Begin Patch\n*** Add File: n\nhello\n*** End Patch",
		"twice":             "*** Begin Patch\n*** Update File: a\n-1\n+2\n*** Delete File: a\n*** End Patch",
		"empty update":      "*** Begin Patch\n*** Update File: a\n*** End Patch",
		"add over a file":   "*** Begin Patch\n*** Add File: a\n+x\n*** End Patch",
		"missing file":      "*** Begin Patch\n*** Delete File: nope\n*** End Patch",
		"no anchor line":    "*** Begin Patch\n*** Update File: a\n@@ func Nope()\n-1\n+2\n*** End Patch",
		"no changes at all": "*** Begin Patch\n*** End Patch",
	}
	for name, input := range cases {
		if _, err := (Patch{e}).Run(context.Background(), args(t, map[string]any{"input": input})); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if readFile(t, filepath.Join(e.Root, "a")) != "1\n" {
		t.Fatal("a refused patch changed a")
	}
}

func TestPatchNeedsACurrentRead(t *testing.T) {
	e := env(t)
	e.Seen = &Seen{}
	write(t, filepath.Join(e.Root, "a"), "1\n")
	p := args(t, patch("*** Update File: a", "-1", "+2"))
	if _, err := (Patch{e}).Run(context.Background(), p); err == nil || !strings.Contains(err.Error(), "read a before updating it") {
		t.Fatalf("unread: %v", err)
	}
	if _, err := (Read{e}).Run(context.Background(), args(t, map[string]any{"path": "a"})); err != nil {
		t.Fatal(err)
	}
	if _, err := (Patch{e}).Run(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	// Its own write leaves the file known.
	if _, err := (Patch{e}).Run(context.Background(), args(t, patch("*** Update File: a", "-2", "+3"))); err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if _, err := (Patch{e}).Run(context.Background(), args(t, patch("*** Add File: b", "+new"))); err != nil {
		t.Fatalf("a new file needs no read: %v", err)
	}
}

func TestPatchPreviewShowsEachFile(t *testing.T) {
	e := env(t)
	write(t, filepath.Join(e.Root, "a"), "1\n")
	out, err := Patch{e}.Preview(args(t, patch("*** Update File: a", "-1", "+2", "*** Add File: b", "+new")))
	if err != nil || !strings.Contains(out, "-1\n+2") || !strings.Contains(out, "--- /dev/null\n+++ b") {
		t.Fatalf("preview %q %v", out, err)
	}
	if readFile(t, filepath.Join(e.Root, "a")) != "1\n" {
		t.Fatal("the preview wrote")
	}
	if l := (Patch{e}).Label(args(t, patch("*** Update File: a", "-1", "+2"))); l != "apply_patch a" {
		t.Fatalf("label %q", l)
	}
}
