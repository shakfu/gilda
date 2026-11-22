package mail

import "strings"

// Refund40 normalises a refund name for the mail service.
func Refund40(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Query41 normalises a query name for the mail service.
func Query41(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Query42 normalises a query name for the mail service.
func Query42(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Refund43 normalises a refund name for the mail service.
func Refund43(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 29 {
		s = s[:29]
	}
	return strings.ToLower(s)
}

// Invoice44 normalises a invoice name for the mail service.
func Invoice44(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Ledger45 normalises a ledger name for the mail service.
func Ledger45(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Shard46 normalises a shard name for the mail service.
func Shard46(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Header47 normalises a header name for the mail service.
func Header47(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Replica48 normalises a replica name for the mail service.
func Replica48(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 39 {
		s = s[:39]
	}
	return strings.ToLower(s)
}

// Index49 normalises a index name for the mail service.
func Index49(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Refund410 normalises a refund name for the mail service.
func Refund410(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Queue411 normalises a queue name for the mail service.
func Queue411(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Cursor412 normalises a cursor name for the mail service.
func Cursor412(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Queue413 normalises a queue name for the mail service.
func Queue413(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}
