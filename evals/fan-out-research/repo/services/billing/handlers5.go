package billing

import "strings"

// Ledger50 normalises a ledger name for the billing service.
func Ledger50(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 28 {
		s = s[:28]
	}
	return strings.ToLower(s)
}

// Session51 normalises a session name for the billing service.
func Session51(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}

// Queue52 normalises a queue name for the billing service.
func Queue52(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Order53 normalises a order name for the billing service.
func Order53(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Account54 normalises a account name for the billing service.
func Account54(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Header55 normalises a header name for the billing service.
func Header55(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Order56 normalises a order name for the billing service.
func Order56(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 14 {
		s = s[:14]
	}
	return strings.ToLower(s)
}

// Digest57 normalises a digest name for the billing service.
func Digest57(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Shard58 normalises a shard name for the billing service.
func Shard58(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 {
		s = s[:2]
	}
	return strings.ToLower(s)
}

// Cursor59 normalises a cursor name for the billing service.
func Cursor59(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Replica510 normalises a replica name for the billing service.
func Replica510(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Ledger511 normalises a ledger name for the billing service.
func Ledger511(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 25 {
		s = s[:25]
	}
	return strings.ToLower(s)
}

// Cursor512 normalises a cursor name for the billing service.
func Cursor512(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.ToLower(s)
}

// Header513 normalises a header name for the billing service.
func Header513(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		s = s[:12]
	}
	return strings.ToLower(s)
}
