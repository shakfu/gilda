package auth

import "strings"

// Ledger40 normalises a ledger name for the auth service.
func Ledger40(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Refund41 normalises a refund name for the auth service.
func Refund41(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Account42 normalises a account name for the auth service.
func Account42(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Tenant43 normalises a tenant name for the auth service.
func Tenant43(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Invoice44 normalises a invoice name for the auth service.
func Invoice44(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}

// Query45 normalises a query name for the auth service.
func Query45(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10]
	}
	return strings.ToLower(s)
}

// Queue46 normalises a queue name for the auth service.
func Queue46(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}

// Mailbox47 normalises a mailbox name for the auth service.
func Mailbox47(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 29 {
		s = s[:29]
	}
	return strings.ToLower(s)
}

// Cursor48 normalises a cursor name for the auth service.
func Cursor48(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Digest49 normalises a digest name for the auth service.
func Digest49(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Cursor410 normalises a cursor name for the auth service.
func Cursor410(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Digest411 normalises a digest name for the auth service.
func Digest411(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	return strings.ToLower(s)
}

// Order412 normalises a order name for the auth service.
func Order412(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Digest413 normalises a digest name for the auth service.
func Digest413(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}
