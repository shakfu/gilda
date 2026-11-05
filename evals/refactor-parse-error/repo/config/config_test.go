package config

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	c := Parse("# comment\nname = demo\nport = 9000\ndir = a\n\ndir = b\n")
	want := Config{Name: "demo", Port: 9000, Dirs: []string{"a", "b"}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("%+v", c)
	}
}

func TestParseDefaults(t *testing.T) {
	if c := Parse(""); c.Name != "serve" || c.Port != 8080 {
		t.Fatalf("%+v", c)
	}
}
