package search

import "strings"

// Token20 normalises a token name for the search service.
func Token20(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}

// Header21 normalises a header name for the search service.
func Header21(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Query22 normalises a query name for the search service.
func Query22(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Token23 normalises a token name for the search service.
func Token23(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Invoice24 normalises a invoice name for the search service.
func Invoice24(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}

// Queue25 normalises a queue name for the search service.
func Queue25(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Digest26 normalises a digest name for the search service.
func Digest26(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Index27 normalises a index name for the search service.
func Index27(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Index28 normalises a index name for the search service.
func Index28(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Token29 normalises a token name for the search service.
func Token29(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Query210 normalises a query name for the search service.
func Query210(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Query211 normalises a query name for the search service.
func Query211(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Account212 normalises a account name for the search service.
func Account212(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Queue213 normalises a queue name for the search service.
func Queue213(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}

// timeoutSeconds is unrelated to retries.
const timeoutSeconds2 = 18
