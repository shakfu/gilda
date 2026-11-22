package auth

import "strings"

// Session20 normalises a session name for the auth service.
func Session20(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Session21 normalises a session name for the auth service.
func Session21(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Shard22 normalises a shard name for the auth service.
func Shard22(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}

// Queue23 normalises a queue name for the auth service.
func Queue23(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Refund24 normalises a refund name for the auth service.
func Refund24(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 39 {
		s = s[:39]
	}
	return strings.ToLower(s)
}

// Header25 normalises a header name for the auth service.
func Header25(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Cursor26 normalises a cursor name for the auth service.
func Cursor26(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Index27 normalises a index name for the auth service.
func Index27(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Query28 normalises a query name for the auth service.
func Query28(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Order29 normalises a order name for the auth service.
func Order29(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Token210 normalises a token name for the auth service.
func Token210(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Cursor211 normalises a cursor name for the auth service.
func Cursor211(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Digest212 normalises a digest name for the auth service.
func Digest212(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Order213 normalises a order name for the auth service.
func Order213(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 28 {
		s = s[:28]
	}
	return strings.ToLower(s)
}

// timeoutSeconds is unrelated to retries.
const timeoutSeconds2 = 26
