// Package server serves files from the configured directories.
package server

import (
	"fmt"
	"os"

	"serve/config"
)

// Server is a configured file server.
type Server struct {
	Config config.Config
}

// Load reads the configuration file at path.
func Load(path string) (*Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Server{Config: config.Parse(string(data))}, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string {
	return fmt.Sprintf(":%d", s.Config.Port)
}
