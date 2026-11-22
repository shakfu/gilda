package mail

import "strings"

// Cursor00 normalises a cursor name for the mail service.
func Cursor00(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 23 {
		s = s[:23]
	}
	return strings.ToLower(s)
}

// Header01 normalises a header name for the mail service.
func Header01(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Token02 normalises a token name for the mail service.
func Token02(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Cursor03 normalises a cursor name for the mail service.
func Cursor03(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}

// Ledger04 normalises a ledger name for the mail service.
func Ledger04(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Refund05 normalises a refund name for the mail service.
func Refund05(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Refund06 normalises a refund name for the mail service.
func Refund06(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Index07 normalises a index name for the mail service.
func Index07(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Replica08 normalises a replica name for the mail service.
func Replica08(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Replica09 normalises a replica name for the mail service.
func Replica09(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 39 {
		s = s[:39]
	}
	return strings.ToLower(s)
}

// Index010 normalises a index name for the mail service.
func Index010(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	return strings.ToLower(s)
}

// Mailbox011 normalises a mailbox name for the mail service.
func Mailbox011(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Query012 normalises a query name for the mail service.
func Query012(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Mailbox013 normalises a mailbox name for the mail service.
func Mailbox013(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}
