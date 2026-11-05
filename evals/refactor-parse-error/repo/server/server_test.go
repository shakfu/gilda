package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.conf")
	os.WriteFile(path, []byte("port = 7000\n"), 0o644)
	s, err := Load(path)
	if err != nil || s.Addr() != ":7000" {
		t.Fatalf("%v %+v", err, s)
	}
}
