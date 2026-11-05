package shell

import (
	"strings"
	"testing"
)

func TestPrograms(t *testing.T) {
	for cmd, want := range map[string]string{
		"rg -n foo .":                     "rg",
		"/opt/homebrew/bin/rg foo":        "rg",
		"cd sub && go test ./...":         "go",
		"(cd sub && make)":                "make",
		"GOFLAGS=-count=1 go test ./...":  "go",
		"env FOO=1 time sed -n 1,5p a.go": "sed",
		"echo hi":                         "echo",
		"   ":                             "(empty)",
		// Loops count their body, without the echo that labels each file.
		`for f in tool/*.go; do echo ==== $f; sed -n '1,280p' "$f"; done`: "sed",
		"while read -r l; do wc -l \"$l\"; done < list":                   "wc",
		"if grep -q x f; then echo yes; fi":                               "grep",
		// Chains and pipelines count each program once.
		"sed -n '1,260p' agent/agent.go; sed -n '1,220p' agent/record.go": "sed",
		"pwd && ls -la && git status --short && find . -maxdepth 2":       "find+git+ls+pwd",
		"nl -ba llm/openai/openai.go | sed -n '42,125p'":                  "nl+sed",
		"go test ./... 2>&1 | tail -3":                                    "go+tail",
		"> out.txt go env":                                                "go",
		// Separators inside quotes and heredoc bodies start nothing.
		`grep -RInE 'TODO|FIXME|panic\(' --exclude-dir=.git .`:                      "grep",
		"cat <<'EOF' > f.go\npackage main; import x\nfunc main() {}\nEOF\ngo build": "cat+go",
		`tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT`:                               "mktemp",
	} {
		if got := strings.Join(Programs(cmd), "+"); got != want {
			t.Errorf("Programs(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestToolName(t *testing.T) {
	for _, c := range [][3]string{
		{"read", "read a.go:1-80", "read"},
		{"bash", "$ go test ./... | tail", "bash go+tail"},
		{"bash", "[task] $ rg foo", "task>bash rg"},
		{"read", "[task] read a.go", "task>read"},
	} {
		if got := ToolName(c[0], c[1]); got != c[2] {
			t.Errorf("ToolName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
