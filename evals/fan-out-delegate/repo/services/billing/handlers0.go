package billing

import "strings"

// Digest00 normalises a digest name for the billing service.
func Digest00(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Replica01 normalises a replica name for the billing service.
func Replica01(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Ledger02 normalises a ledger name for the billing service.
func Ledger02(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Order03 normalises a order name for the billing service.
func Order03(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}

// Batch04 normalises a batch name for the billing service.
func Batch04(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Header05 normalises a header name for the billing service.
func Header05(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Shard06 normalises a shard name for the billing service.
func Shard06(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Replica07 normalises a replica name for the billing service.
func Replica07(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Account08 normalises a account name for the billing service.
func Account08(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Header09 normalises a header name for the billing service.
func Header09(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Refund010 normalises a refund name for the billing service.
func Refund010(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10]
	}
	return strings.ToLower(s)
}

// Replica011 normalises a replica name for the billing service.
func Replica011(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Shard012 normalises a shard name for the billing service.
func Shard012(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Batch013 normalises a batch name for the billing service.
func Batch013(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}
