package slug

import "testing"

func TestMake(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":     "hello-world",
		"  Go 1.22 notes  ": "go-1-22-notes",
		"already-a-slug":    "already-a-slug",
	} {
		if got := Make(in); got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
	}
}
