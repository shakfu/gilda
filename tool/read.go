package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/shakfu/gilda/llm"
)

type Read struct{ Env }

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (r Read) Spec() llm.ToolSpec {
	n := r.limits().ReadLines
	return llm.ToolSpec{
		Name: "read",
		Description: fmt.Sprintf("Read a text file as numbered lines, or list a directory's entries, one per line. "+
			"Returns at most %d lines; use offset and limit for more.", n),
		Schema: schema([]string{"path"}, map[string]any{
			"path":   prop("string", "File or directory path, absolute or relative to the working directory."),
			"offset": prop("integer", "First line, 1-based. Default 1."),
			"limit":  prop("integer", fmt.Sprintf("Maximum lines. Default %d.", n)),
		}),
	}
}

func (Read) Label(raw json.RawMessage) string {
	var a readArgs
	_ = decode(raw, &a)
	if a.Offset > 0 || a.Limit > 0 {
		start := max(a.Offset, 1)
		end := "end"
		if a.Limit > 0 {
			end = fmt.Sprint(start + a.Limit - 1)
		}
		return fmt.Sprintf("read %s:%d-%s", a.Path, start, end)
	}
	return "read " + a.Path
}

// Run streams the file, so memory follows the lines returned rather than the file size.
func (r Read) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	var a readArgs
	if err := decode(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, fmt.Errorf("path is required")
	}
	f, info, err := openRead(r.abs(a.Path), a.Path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	if info.IsDir() {
		return r.list(f, a)
	}
	r.Seen.record(r.abs(a.Path), info)

	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(8 << 10)
	if mt := imageType(head); mt != "" {
		return readImage(br, info, a.Path, mt)
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return Result{Output: fmt.Sprintf("%s is binary, %d bytes", a.Path, info.Size()), Summary: "binary"}, nil
	}

	start := max(a.Offset, 1)
	lim := r.limits()
	limit := lim.ReadLines
	if a.Limit > 0 {
		limit = min(a.Limit, lim.ReadLines)
	}
	var out strings.Builder
	total, shown, cut := 0, 0, false
	for {
		// ReadLineBytes bounds one line; minified files otherwise fill the result with one line.
		line, long, err := readLine(br, lim.ReadLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{}, err
		}
		total++
		if total%4096 == 0 && ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		// Lines past the window are still counted, so the footer can say how many are left.
		if total >= start && shown < limit && out.Len() < lim.OutputCap {
			fmt.Fprintf(&out, "%6d\t%s\n", total, line)
			shown++
			cut = cut || long
		}
	}
	if shown == 0 {
		msg := fmt.Sprintf("%s has %d lines; none at offset %d", a.Path, total, start)
		return Result{Output: msg, Summary: plural(0, "line")}, nil
	}
	if last := start + shown - 1; last < total {
		fmt.Fprintf(&out, "... %s not shown; continue with offset %d\n", plural(total-last, "line"), last+1)
	}
	if cut {
		fmt.Fprintf(&out, "... long lines were cut at %d bytes\n", lim.ReadLineBytes)
	}
	return Result{Output: out.String(), Summary: plural(shown, "line")}, nil
}

// ImageMax bounds an image read returns, in bytes. Anthropic refuses an image over 5 MB
// encoded, which base64 reaches at about 3.75 MB.
const ImageMax = 3 << 20

// imageType names the image format head starts with, or "" for anything else.
func imageType(head []byte) string {
	switch t := http.DetectContentType(head); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t
	}
	return ""
}

// readImage returns the image for the model to see, with a line describing it.
func readImage(r io.Reader, info os.FileInfo, name, mediaType string) (Result, error) {
	kind := strings.ToUpper(strings.TrimPrefix(mediaType, "image/"))
	if info.Size() > ImageMax {
		return Result{Output: fmt.Sprintf("%s is a %s image of %s, over the %d MiB that read sends", name, kind,
			plural(int(info.Size()), "byte"), ImageMax>>20), Summary: "image too large"}, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, ImageMax+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > ImageMax {
		return Result{Output: fmt.Sprintf("%s grew past %d MiB while it was read", name, ImageMax>>20), Summary: "image too large"}, nil
	}
	size, summary := "", "image"
	if c, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		size = fmt.Sprintf(", %dx%d", c.Width, c.Height)
		summary = fmt.Sprintf("image %dx%d", c.Width, c.Height)
	}
	return Result{
		Output:  fmt.Sprintf("%s is a %s image%s, %s; it follows as an image", name, kind, size, plural(len(data), "byte")),
		Summary: summary,
		Images:  []llm.Image{{MediaType: mediaType, Data: data}},
	}, nil
}

// list returns a directory's entries, sorted, one per numbered line, with offset and limit
// applied as to lines. A directory name ends in / and a symlink's in @, as ls -F marks them.
func (r Read) list(f *os.File, a readArgs) (Result, error) {
	entries, err := f.ReadDir(-1)
	if err != nil {
		return Result{}, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
		switch {
		case e.IsDir():
			names[i] += "/"
		case e.Type()&fs.ModeSymlink != 0:
			names[i] += "@"
		}
	}
	sort.Strings(names)
	lim := r.limits()
	start, limit := max(a.Offset, 1), lim.ReadLines
	if a.Limit > 0 {
		limit = min(a.Limit, lim.ReadLines)
	}
	if start > len(names) {
		msg := fmt.Sprintf("%s has %s; none at offset %d", a.Path, countEntries(len(names)), start)
		return Result{Output: msg, Summary: countEntries(0)}, nil
	}
	var out strings.Builder
	shown := 0
	for i := start - 1; i < len(names) && shown < limit && out.Len() < lim.OutputCap; i++ {
		fmt.Fprintf(&out, "%6d\t%s\n", i+1, names[i])
		shown++
	}
	if last := start + shown - 1; last < len(names) {
		fmt.Fprintf(&out, "... %s not shown; continue with offset %d\n", countEntries(len(names)-last), last+1)
	}
	return Result{Output: out.String(), Summary: countEntries(shown)}, nil
}

func countEntries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// openRead opens path as openRegular does, but also accepts a directory.
func openRead(path, name string) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() && !info.IsDir() {
		err = fmt.Errorf("%s is not a regular file or a directory", name)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// openRegular opens path for reading and refuses anything but a regular file: a device has no
// end, and a directory fails with a less helpful error. O_NONBLOCK keeps opening a FIFO from
// waiting for a writer; the check is on the opened file, so a swap after it cannot slip past.
func openRegular(path, name string) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", name)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// readLine returns one line without its newline or carriage return, cut at lineCap bytes, and
// whether it was cut. It never holds more than lineCap bytes of a line.
func readLine(br *bufio.Reader, lineCap int) (string, bool, error) {
	var buf []byte
	long := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (buf != nil || long) {
				break
			}
			return "", false, err
		}
		if room := lineCap - len(buf); room > 0 {
			if len(chunk) > room {
				chunk, long = chunk[:room], true
			}
			buf = append(buf, chunk...)
		} else {
			long = true
		}
		if !isPrefix {
			break
		}
	}
	return string(bytes.TrimSuffix(buf, []byte("\r"))), long, nil
}
