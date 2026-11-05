package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editFile(t *testing.T, content, old, new string) (string, string, error) {
	t.Helper()
	e := env(t)
	path := filepath.Join(e.Root, "f.go")
	write(t, path, content)
	res, err := Edit{e}.Run(context.Background(), args(t, map[string]any{"path": "f.go", "old_string": old, "new_string": new}))
	data, _ := os.ReadFile(path)
	return string(data), res.Output, err
}

func TestEditIgnoresTrailingWhitespaceAndSaysSo(t *testing.T) {
	got, out, err := editFile(t, "func f() {  \n\treturn 1\n}\n", "func f() {\n\treturn 1", "func f() {\n\treturn 2")
	if err != nil || got != "func f() {\n\treturn 2\n}\n" || !strings.Contains(out, "ignoring trailing whitespace") {
		t.Fatalf("%v %q %q", err, got, out)
	}
}

// A block sent without its indentation, or with spaces for tabs, is reindented to the file's.
func TestEditReindentsABlockMatchedIgnoringIndentation(t *testing.T) {
	file := "func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n"
	got, out, err := editFile(t, file, "if x {\n\treturn 1\n}\n", "if x {\n\treturn 2\n}\nreturn 3\n")
	want := "func f() {\n\tif x {\n\t\treturn 2\n\t}\n\treturn 3\n}\n"
	if err != nil || got != want || !strings.Contains(out, "reindented") {
		t.Fatalf("%v\n%q\n%q", err, got, out)
	}
	got, _, err = editFile(t, file, "    if x {\n    \treturn 1", "    if y {\n    \treturn 1")
	if err != nil || got != "func f() {\n\tif y {\n\t\treturn 1\n\t}\n}\n" {
		t.Fatalf("%v %q", err, got)
	}
}

func TestEditRefusesALooseMatchThatIsNotUnique(t *testing.T) {
	_, _, err := editFile(t, "\ta()\n\tb()\n\ta()\n\tb()\n", "a()\nb()", "c()")
	if err == nil || !strings.Contains(err.Error(), "matches 2 places (lines 1, 3)") {
		t.Fatalf("%v", err)
	}
}

func TestEditRefusesNewStringIndentedUnlikeOldString(t *testing.T) {
	got, _, err := editFile(t, "\tif x {\n\t\ty()\n\t}\n", "  if x {\n  \ty()", "if x {\n\tz()")
	if err == nil || !strings.Contains(err.Error(), "not indented like old_string") || got != "\tif x {\n\t\ty()\n\t}\n" {
		t.Fatalf("%v %q", err, got)
	}
}

func TestEditQuotesTheNearestText(t *testing.T) {
	file := "package p\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n"
	_, _, err := editFile(t, file, "func add(a int, b int) int {\n\treturn a+b\n", "x")
	if err == nil || !strings.Contains(err.Error(), "nearest text is at lines 3-4:\n     3\tfunc add(a, b int) int {\n     4\t\treturn a + b") {
		t.Fatalf("%v", err)
	}
	_, _, err = editFile(t, file, "something else entirely", "x")
	if err == nil || strings.Contains(err.Error(), "nearest") {
		t.Fatalf("unrelated text: %v", err)
	}
}

func TestEditNamesReadsLineNumberPrefixes(t *testing.T) {
	_, _, err := editFile(t, "a\nb\n", "     1\ta\n     2\tb", "x")
	if err == nil || !strings.Contains(err.Error(), "line-number prefixes") {
		t.Fatalf("%v", err)
	}
}

func TestEditLooseMatchKeepsCRLF(t *testing.T) {
	got, _, err := editFile(t, "\tone\r\n\ttwo\r\nthree\r\n", "one\ntwo\n", "1\n2\n")
	if err != nil || got != "\t1\r\n\t2\r\nthree\r\n" {
		t.Fatalf("%v %q", err, got)
	}
}
