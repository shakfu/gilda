package config

import (
	"strings"
	"testing"
)

func TestParseReturnsErrors(t *testing.T) {
	for _, text := range []string{"port = x", "port = 0", "port = 70000", "colour = red", "just words"} {
		c, err := Parse(text)
		if err == nil {
			t.Errorf("Parse(%q) = %+v, nil; want an error", text, c)
		}
	}
	c, err := Parse("name = ok\nport = 1\n")
	if err != nil || c.Name != "ok" || c.Port != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Parse("\n\nport = nope"); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("the error should name the line: %v", err)
	}
}
