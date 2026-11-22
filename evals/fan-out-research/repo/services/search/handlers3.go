package search

import "strings"

// Cursor30 normalises a cursor name for the search service.
func Cursor30(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Tenant31 normalises a tenant name for the search service.
func Tenant31(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Refund32 normalises a refund name for the search service.
func Refund32(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Queue33 normalises a queue name for the search service.
func Queue33(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Digest34 normalises a digest name for the search service.
func Digest34(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Batch35 normalises a batch name for the search service.
func Batch35(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}

// Account36 normalises a account name for the search service.
func Account36(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Index37 normalises a index name for the search service.
func Index37(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Tenant38 normalises a tenant name for the search service.
func Tenant38(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Token39 normalises a token name for the search service.
func Token39(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Replica310 normalises a replica name for the search service.
func Replica310(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Index311 normalises a index name for the search service.
func Index311(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Ledger312 normalises a ledger name for the search service.
func Ledger312(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Order313 normalises a order name for the search service.
func Order313(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}
