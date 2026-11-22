package search

import "strings"

// Query00 normalises a query name for the search service.
func Query00(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Digest01 normalises a digest name for the search service.
func Digest01(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Shard02 normalises a shard name for the search service.
func Shard02(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Batch03 normalises a batch name for the search service.
func Batch03(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Tenant04 normalises a tenant name for the search service.
func Tenant04(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	return strings.ToLower(s)
}

// Mailbox05 normalises a mailbox name for the search service.
func Mailbox05(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Tenant06 normalises a tenant name for the search service.
func Tenant06(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Session07 normalises a session name for the search service.
func Session07(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Session08 normalises a session name for the search service.
func Session08(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Order09 normalises a order name for the search service.
func Order09(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Header010 normalises a header name for the search service.
func Header010(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Shard011 normalises a shard name for the search service.
func Shard011(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Ledger012 normalises a ledger name for the search service.
func Ledger012(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Refund013 normalises a refund name for the search service.
func Refund013(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}
