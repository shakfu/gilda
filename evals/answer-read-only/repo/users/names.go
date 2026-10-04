package users

import "strings"

// isValidName rejects empty names.
func isValidName(s string) bool { return strings.TrimSpace(s) != "" }
