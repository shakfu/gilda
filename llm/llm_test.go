package llm

import (
	"encoding/json"
	"testing"
)

func TestRequiredFromGoOrJSON(t *testing.T) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","required":["path","content"]}`), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, schema := range []map[string]any{decoded, {"required": []string{"path", "content"}}} {
		if r := (ToolSpec{Schema: schema}).Required(); len(r) != 2 || r[0] != "path" || r[1] != "content" {
			t.Errorf("required %v from %T", r, schema["required"])
		}
	}
	if r := (ToolSpec{Schema: map[string]any{}}).Required(); r != nil {
		t.Errorf("no required gave %v", r)
	}
}

// A raw tab in a string, as LFM2.5 emitted, fails llama-server's parse of every later request.
func TestArgsFallsBackToAnEmptyObject(t *testing.T) {
	for in, want := range map[string]string{`{"a":1}`: `{"a":1}`, `{"a":`: `{}`, ``: `{}`, `[1]`: `{}`, "{\"a\":\"x\ty\"}": `{}`} {
		if got := Args(in); got != want {
			t.Errorf("Args(%q) = %q, want %q", in, got, want)
		}
	}
}
