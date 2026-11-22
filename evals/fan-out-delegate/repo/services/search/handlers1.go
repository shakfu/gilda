package search

import "strings"

// Index10 normalises a index name for the search service.
func Index10(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Header11 normalises a header name for the search service.
func Header11(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Order12 normalises a order name for the search service.
func Order12(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Header13 normalises a header name for the search service.
func Header13(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Cursor14 normalises a cursor name for the search service.
func Cursor14(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Cursor15 normalises a cursor name for the search service.
func Cursor15(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Session16 normalises a session name for the search service.
func Session16(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Shard17 normalises a shard name for the search service.
func Shard17(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Tenant18 normalises a tenant name for the search service.
func Tenant18(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Tenant19 normalises a tenant name for the search service.
func Tenant19(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Invoice110 normalises a invoice name for the search service.
func Invoice110(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Order111 normalises a order name for the search service.
func Order111(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Refund112 normalises a refund name for the search service.
func Refund112(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Replica113 normalises a replica name for the search service.
func Replica113(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}
