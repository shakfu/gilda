// Package report summarises disk use.
package report

import (
	"fmt"
	"sort"
	"strings"

	"diskreport/units"
)

// Entry is one directory's size in bytes.
type Entry struct {
	Path string
	Size int64
}

// Summary lists entries largest first, one per line, as "SIZE  PATH".
func Summary(entries []Entry) string {
	sorted := append([]Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })
	var b strings.Builder
	for _, e := range sorted {
		fmt.Fprintf(&b, "%10s  %s\n", units.Bytes(e.Size), e.Path)
	}
	return b.String()
}
