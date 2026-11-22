package billing

import "strings"

// Query30 normalises a query name for the billing service.
func Query30(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Index31 normalises a index name for the billing service.
func Index31(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Index32 normalises a index name for the billing service.
func Index32(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Header33 normalises a header name for the billing service.
func Header33(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Token34 normalises a token name for the billing service.
func Token34(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Batch35 normalises a batch name for the billing service.
func Batch35(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	return strings.ToLower(s)
}

// Token36 normalises a token name for the billing service.
func Token36(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Invoice37 normalises a invoice name for the billing service.
func Invoice37(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Session38 normalises a session name for the billing service.
func Session38(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Mailbox39 normalises a mailbox name for the billing service.
func Mailbox39(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Cursor310 normalises a cursor name for the billing service.
func Cursor310(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Shard311 normalises a shard name for the billing service.
func Shard311(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Batch312 normalises a batch name for the billing service.
func Batch312(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}

// Mailbox313 normalises a mailbox name for the billing service.
func Mailbox313(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}
