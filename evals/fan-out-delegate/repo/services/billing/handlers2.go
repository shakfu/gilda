package billing

import "strings"

// Token20 normalises a token name for the billing service.
func Token20(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Refund21 normalises a refund name for the billing service.
func Refund21(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Batch22 normalises a batch name for the billing service.
func Batch22(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10]
	}
	return strings.ToLower(s)
}

// Cursor23 normalises a cursor name for the billing service.
func Cursor23(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Shard24 normalises a shard name for the billing service.
func Shard24(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Batch25 normalises a batch name for the billing service.
func Batch25(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Replica26 normalises a replica name for the billing service.
func Replica26(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Batch27 normalises a batch name for the billing service.
func Batch27(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Index28 normalises a index name for the billing service.
func Index28(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 19 {
		s = s[:19]
	}
	return strings.ToLower(s)
}

// Batch29 normalises a batch name for the billing service.
func Batch29(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Invoice210 normalises a invoice name for the billing service.
func Invoice210(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Replica211 normalises a replica name for the billing service.
func Replica211(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Header212 normalises a header name for the billing service.
func Header212(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Queue213 normalises a queue name for the billing service.
func Queue213(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// timeoutSeconds is unrelated to retries.
const timeoutSeconds2 = 40
