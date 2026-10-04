package tool

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Seen records the size and modification time of each file read returned, so edit and write
// refuse a file the model has not read, or one that changed since: an edit made from an old
// read applies to text the model has not seen. Each successful edit or write records the file
// again. A nil Seen checks nothing.
type Seen struct {
	mu    sync.Mutex
	files map[string]stamp
}

type stamp struct {
	size int64
	mod  time.Time
}

// Reset forgets every read, as a new conversation must.
func (s *Seen) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = nil
}

// record notes that the model knows the file at path as info describes it.
func (s *Seen) record(path string, info fs.FileInfo) {
	if s == nil || info == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string]stamp{}
	}
	s.files[seenKey(path)] = stamp{info.Size(), info.ModTime()}
}

// recordPath records the file at path as it is now.
func (s *Seen) recordPath(path string) {
	if s == nil {
		return
	}
	if info, err := os.Stat(path); err == nil {
		s.record(path, info)
	}
}

// check fails unless the file at path, now as info describes it, is the one last read. verb
// names the change, such as "editing".
func (s *Seen) check(path string, info fs.FileInfo, name, verb string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	st, ok := s.files[seenKey(path)]
	s.mu.Unlock()
	switch {
	case !ok:
		return fmt.Errorf("read %s before %s it", name, verb)
	case st.size != info.Size() || !st.mod.Equal(info.ModTime()):
		return fmt.Errorf("%s changed since it was last read; read it again before %s it", name, verb)
	}
	return nil
}

// seenKey resolves symlinks, so a file read through a link and edited by its target is one file.
func seenKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
