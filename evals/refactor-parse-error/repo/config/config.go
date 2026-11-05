// Package config reads the server's settings.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Config holds the server's settings.
type Config struct {
	Name string
	Port int
	Dirs []string
}

// Parse reads "key = value" lines. Blank lines and lines starting with # are skipped. Keys
// are name, port and dir; dir may repeat. It panics on a malformed line, an unknown key or a
// port that is not a number from 1 to 65535.
func Parse(text string) Config {
	c := Config{Name: "serve", Port: 8080}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			panic(fmt.Sprintf("line %d: no '='", i+1))
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "name":
			c.Name = value
		case "port":
			p, err := strconv.Atoi(value)
			if err != nil || p < 1 || p > 65535 {
				panic(fmt.Sprintf("line %d: bad port %q", i+1, value))
			}
			c.Port = p
		case "dir":
			c.Dirs = append(c.Dirs, value)
		default:
			panic(fmt.Sprintf("line %d: unknown key %q", i+1, key))
		}
	}
	return c
}
