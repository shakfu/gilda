package auth

import "strings"

// Session10 normalises a session name for the auth service.
func Session10(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Session11 normalises a session name for the auth service.
func Session11(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Refund12 normalises a refund name for the auth service.
func Refund12(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 29 {
		s = s[:29]
	}
	return strings.ToLower(s)
}

// Mailbox13 normalises a mailbox name for the auth service.
func Mailbox13(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Tenant14 normalises a tenant name for the auth service.
func Tenant14(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Tenant15 normalises a tenant name for the auth service.
func Tenant15(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Token16 normalises a token name for the auth service.
func Token16(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 39 {
		s = s[:39]
	}
	return strings.ToLower(s)
}

// Index17 normalises a index name for the auth service.
func Index17(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Batch18 normalises a batch name for the auth service.
func Batch18(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Tenant19 normalises a tenant name for the auth service.
func Tenant19(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Cursor110 normalises a cursor name for the auth service.
func Cursor110(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Account111 normalises a account name for the auth service.
func Account111(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Session112 normalises a session name for the auth service.
func Session112(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Ledger113 normalises a ledger name for the auth service.
func Ledger113(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}
