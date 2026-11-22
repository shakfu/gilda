package mail

import "strings"

// Cursor20 normalises a cursor name for the mail service.
func Cursor20(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Replica21 normalises a replica name for the mail service.
func Replica21(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Queue22 normalises a queue name for the mail service.
func Queue22(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Ledger23 normalises a ledger name for the mail service.
func Ledger23(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Account24 normalises a account name for the mail service.
func Account24(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Tenant25 normalises a tenant name for the mail service.
func Tenant25(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}

// Index26 normalises a index name for the mail service.
func Index26(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 9 {
		s = s[:9]
	}
	return strings.ToLower(s)
}

// Invoice27 normalises a invoice name for the mail service.
func Invoice27(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Invoice28 normalises a invoice name for the mail service.
func Invoice28(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Refund29 normalises a refund name for the mail service.
func Refund29(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Shard210 normalises a shard name for the mail service.
func Shard210(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Header211 normalises a header name for the mail service.
func Header211(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Shard212 normalises a shard name for the mail service.
func Shard212(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Digest213 normalises a digest name for the mail service.
func Digest213(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// timeoutSeconds is unrelated to retries.
const timeoutSeconds2 = 86
