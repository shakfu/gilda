package tool

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// target is a file that write or edit resolved when its call was bound: the nearest existing
// directory, the rest of the path under it, and the file that was there, if any. Writing
// through it fails if any of these changed, so a symlink swapped after approval cannot
// redirect the write.
type target struct {
	name string // the path as the call gave it, for messages
	path string // absolute, with symlinks resolved on its existing part
	dir  string
	// dirInfo is dir as bound; rest is the path under dir, the last element naming the file.
	dirInfo fs.FileInfo
	rest    []string
	// file is the file as bound, or nil when there was none.
	file fs.FileInfo
}

// bind resolves abs, following symlinks on its longest existing prefix, and records what it
// found. It creates nothing. A path to anything but a regular file is an error.
func bind(abs, name string) (target, error) {
	t := target{name: name}
	p := filepath.Clean(abs)
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			p = real
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return t, err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return t, err
		}
		t.rest = append([]string{filepath.Base(p)}, t.rest...)
		p = parent
	}
	t.path = filepath.Join(append([]string{p}, t.rest...)...)
	t.dir = p
	if len(t.rest) == 0 {
		info, err := os.Lstat(p)
		if err != nil {
			return t, err
		}
		if !info.Mode().IsRegular() {
			return t, fmt.Errorf("%s is not a regular file", name)
		}
		t.file = info
		t.dir, t.rest = filepath.Dir(p), []string{filepath.Base(p)}
	}
	info, err := os.Lstat(t.dir)
	if err != nil {
		return t, err
	}
	t.dirInfo = info
	return t, nil
}

func (t target) changed() error {
	return fmt.Errorf("%s changed after the call was checked; try again", t.name)
}

// same reports whether info is the file as bound: the same inode, size and modification time.
func (t target) same(info fs.FileInfo) bool {
	return os.SameFile(info, t.file) && info.Size() == t.file.Size() && info.ModTime().Equal(t.file.ModTime())
}

// openRoot opens the directory the file goes in, creating the missing ones. Each directory is
// checked after it is opened, so one swapped for a symlink or another directory is refused.
func (t target) openRoot() (*os.Root, error) {
	r, err := os.OpenRoot(t.dir)
	if err != nil {
		return nil, err
	}
	if info, err := r.Stat("."); err != nil || !os.SameFile(info, t.dirInfo) {
		r.Close()
		return nil, t.changed()
	}
	for _, c := range t.rest[:len(t.rest)-1] {
		if err := r.Mkdir(c, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			r.Close()
			return nil, err
		}
		sub, err := enter(r, c)
		r.Close()
		if err != nil {
			return nil, t.changed()
		}
		r = sub
	}
	return r, nil
}

// enter opens the directory c under r, refusing a symlink or a swap between the check and the open.
func enter(r *os.Root, c string) (*os.Root, error) {
	info, err := r.Lstat(c)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", c)
	}
	sub, err := r.OpenRoot(c)
	if err != nil {
		return nil, err
	}
	if got, err := sub.Stat("."); err != nil || !os.SameFile(got, info) {
		sub.Close()
		return nil, fmt.Errorf("%s changed", c)
	}
	return sub, nil
}

// check fails unless the file under r is still the one bound. before, when not nil, must also
// match its content.
func (t target) check(r *os.Root, before []byte) error {
	base := t.rest[len(t.rest)-1]
	info, err := r.Lstat(base)
	switch {
	case t.file == nil && errors.Is(err, fs.ErrNotExist):
		return nil
	case t.file == nil, err != nil, !t.same(info):
		return t.changed()
	case before == nil:
		return nil
	}
	f, err := r.OpenFile(base, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return t.changed()
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !t.same(info) {
		return t.changed()
	}
	// One byte more than before is enough to tell a longer file apart.
	data, err := io.ReadAll(io.LimitReader(f, int64(len(before))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, before) {
		return t.changed()
	}
	return nil
}

// write replaces the file through a temporary file and a rename, so a crash or a full disk
// never leaves half a file where the only copy was. It keeps an existing file's mode. before
// is passed to check.
func (t target) write(data, before []byte) error {
	r, err := t.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := t.check(r, before); err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if t.file != nil {
		mode = t.file.Mode().Perm()
	}
	base := t.rest[len(t.rest)-1]
	tmp := "." + base + ".gilda-" + strconv.FormatUint(rand.Uint64(), 36)
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	// The rename replaces the file, so its owner is copied where the user may set it, as when
	// running as root. ACLs, extended attributes and hard links are not kept.
	if t.file != nil {
		if st, ok := t.file.Sys().(*syscall.Stat_t); ok {
			_ = f.Chown(int(st.Uid), int(st.Gid))
		}
	}
	// Synced before the rename, so a crash leaves the old file or the new one, never an empty one.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Rename(tmp, base); err != nil {
		return err
	}
	// The directory holds the rename; syncing it makes the rename survive a crash too.
	if d, err := r.Open("."); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// EditMax bounds the file edit and apply_patch change, in bytes; each holds it twice in memory.
const EditMax = 16 << 20

// readForEdit reads a file edit or apply_patch is about to change, refusing one over EditMax.
func readForEdit(f *os.File, info fs.FileInfo, name string) ([]byte, error) {
	if info.Size() > EditMax {
		return nil, fmt.Errorf("%s is %s, over the %d MiB edit and apply_patch change; use bash",
			name, plural(int(info.Size()), "byte"), EditMax>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, EditMax+1))
	if err == nil && len(data) > EditMax {
		err = fmt.Errorf("%s grew past %d MiB while it was read", name, EditMax>>20)
	}
	return data, err
}

// remove deletes the file, failing unless it is still the one bound and, when before is not
// nil, still holds before.
func (t target) remove(before []byte) error {
	if t.file == nil {
		return fmt.Errorf("%s does not exist", t.name)
	}
	r, err := t.openRoot()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := t.check(r, before); err != nil {
		return err
	}
	return r.Remove(t.rest[len(t.rest)-1])
}

// paths are what approval checks: the path as the call gave it and as bound, when they differ.
func (t target) paths() []string {
	if t.path == t.name {
		return []string{t.name}
	}
	return []string{t.name, t.path}
}
