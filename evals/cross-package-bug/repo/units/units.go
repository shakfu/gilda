// Package units formats quantities for people.
package units

import "fmt"

var suffixes = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// Bytes formats n bytes with a binary suffix and one decimal, such as "1.5 KiB". Values under
// 1 KiB are whole bytes, such as "512 B".
func Bytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	i := 0
	for v > 1024 && i < len(suffixes)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, suffixes[i])
}
