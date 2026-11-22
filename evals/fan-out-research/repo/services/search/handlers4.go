package search

import "strings"

// Shard40 normalises a shard name for the search service.
func Shard40(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Refund41 normalises a refund name for the search service.
func Refund41(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Ledger42 normalises a ledger name for the search service.
func Ledger42(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Header43 normalises a header name for the search service.
func Header43(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Account44 normalises a account name for the search service.
func Account44(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Cursor45 normalises a cursor name for the search service.
func Cursor45(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Index46 normalises a index name for the search service.
func Index46(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Token47 normalises a token name for the search service.
func Token47(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Query48 normalises a query name for the search service.
func Query48(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Tenant49 normalises a tenant name for the search service.
func Tenant49(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Header410 normalises a header name for the search service.
func Header410(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 23 {
		s = s[:23]
	}
	return strings.ToLower(s)
}

// Shard411 normalises a shard name for the search service.
func Shard411(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Account412 normalises a account name for the search service.
func Account412(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Order413 normalises a order name for the search service.
func Order413(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 28 {
		s = s[:28]
	}
	return strings.ToLower(s)
}
