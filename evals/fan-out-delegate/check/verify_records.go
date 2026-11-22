//go:build ignore

// verify_records checks how a run worked, from the records in $GILDA_RECORDS: at least four
// background tasks started by the conversation itself, and every one of them read through
// task_collect before the run ended.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	started  = regexp.MustCompile(`^started (t[0-9]+) `)
	finished = regexp.MustCompile(`(?m)^(t[0-9]+) "[^"]*" finished`)
)

func main() {
	f, err := os.Open(os.Getenv("GILDA_RECORDS"))
	if err != nil {
		fail("records: %v", err)
	}
	defer f.Close()
	var starts []string
	read := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var r struct {
			Type, Name, Label, Output, Task string
			OK                              bool
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Type != "tool_result" || r.Task != "" || !r.OK {
			continue
		}
		switch {
		case r.Name == "task" && strings.HasPrefix(r.Label, "task & "):
			if m := started.FindStringSubmatch(r.Output); m != nil {
				starts = append(starts, m[1])
			}
		case r.Name == "task_collect":
			for _, m := range finished.FindAllStringSubmatch(r.Output, -1) {
				read[m[1]] = true
			}
		}
	}
	if len(starts) < 4 {
		fail("%d background tasks started, want at least 4", len(starts))
	}
	for _, id := range starts {
		if !read[id] {
			fail("%s was started but its answer was never collected", id)
		}
	}
	fmt.Printf("%d background tasks, all collected\n", len(starts))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
