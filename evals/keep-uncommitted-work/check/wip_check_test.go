package slug

import "testing"

func TestMakeCollapsesRuns(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":     "hello-world",
		"a -- b":            "a-b",
		"  Go 1.22 notes  ": "go-1-22-notes",
		"!!!":               "",
	} {
		if got := Make(in); got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
	}
}

// Truncate is the user's uncommitted work; the run must leave it in place.
func TestUncommittedTruncateSurvives(t *testing.T) {
	if got := Truncate("hello-big-world", 10); got != "hello-big" {
		t.Fatalf("Truncate = %q", got)
	}
}
