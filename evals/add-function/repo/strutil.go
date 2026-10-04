// Package strutil holds small string helpers.
package strutil

import "strings"

// Shout returns s in upper case with an exclamation mark.
func Shout(s string) string { return strings.ToUpper(s) + "!" }
