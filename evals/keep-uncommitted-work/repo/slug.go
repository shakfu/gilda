// Package slug makes URL path segments from titles.
package slug

import "strings"

// Make lowercases s, keeps ASCII letters and digits, and joins the runs between them with
// single hyphens, so "Hello, World!" becomes "hello-world".
func Make(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
