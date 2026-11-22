package auth

import "strings"

// Replica50 normalises a replica name for the auth service.
func Replica50(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Query51 normalises a query name for the auth service.
func Query51(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Account52 normalises a account name for the auth service.
func Account52(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10]
	}
	return strings.ToLower(s)
}

// Token53 normalises a token name for the auth service.
func Token53(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Invoice54 normalises a invoice name for the auth service.
func Invoice54(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Batch55 normalises a batch name for the auth service.
func Batch55(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Batch56 normalises a batch name for the auth service.
func Batch56(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Token57 normalises a token name for the auth service.
func Token57(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Index58 normalises a index name for the auth service.
func Index58(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Cursor59 normalises a cursor name for the auth service.
func Cursor59(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Cursor510 normalises a cursor name for the auth service.
func Cursor510(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Batch511 normalises a batch name for the auth service.
func Batch511(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Query512 normalises a query name for the auth service.
func Query512(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Queue513 normalises a queue name for the auth service.
func Queue513(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}
