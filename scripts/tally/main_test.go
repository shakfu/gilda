package main

import (
	"strings"
	"testing"
)

// Results count under the model named by the result record that ends their run.
func TestTally(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"tool_call","name":"bash","label":"$ cat a.go"}`,
		`{"type":"tool_result","name":"bash","label":"$ cat a.go","ok":true,"output":"12345"}`,
		`{"type":"tool_result","name":"read","label":"read b.go","ok":true,"output":"123"}`,
		`{"type":"result","provider":"openai","model":"m1"}`,
		`{"type":"tool_result","name":"bash","label":"$ cat c.go","ok":false,"output":"12"}`,
		`{"type":"tool_result","name":"bash","label":"$ cat d.go","ok":true,"output":"1"}`,
		`{"type":"result","provider":"anthropic","model":"m2"}`,
		`{"type":"tool_result","name":"edit","label":"edit x","ok":true,"output":""}`,
	}, "\n")
	totals := map[key]*count{}
	if err := tally(strings.NewReader(input), totals); err != nil {
		t.Fatal(err)
	}
	want := map[key]count{
		{"openai/m1", "bash cat"}:    {1, 0, 5},
		{"openai/m1", "read"}:        {1, 0, 3},
		{"anthropic/m2", "bash cat"}: {2, 1, 3},
		{"unknown", "edit"}:          {1, 0, 0},
	}
	if len(totals) != len(want) {
		t.Fatalf("got %d rows, want %d", len(totals), len(want))
	}
	for k, w := range want {
		if c := totals[k]; c == nil || *c != w {
			t.Errorf("%v: got %+v, want %+v", k, c, w)
		}
	}
	var out strings.Builder
	report(&out, totals)
	if !strings.Contains(out.String(), "62%") || !strings.Contains(out.String(), "bash cat") {
		t.Errorf("report:\n%s", out.String())
	}
}

func TestBadLineIsAnError(t *testing.T) {
	if err := tally(strings.NewReader("{}\nnot json\n"), map[key]*count{}); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("got %v", err)
	}
}
