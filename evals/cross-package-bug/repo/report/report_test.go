package report

import "testing"

func TestSummary(t *testing.T) {
	got := Summary([]Entry{
		{"src", 512},
		{"cache", 1024},
		{"logs", 1536},
		{"media", 1 << 20},
	})
	want := "   1.0 MiB  media\n" +
		"   1.5 KiB  logs\n" +
		"   1.0 KiB  cache\n" +
		"     512 B  src\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
