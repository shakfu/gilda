package billing

import "strings"

// Refund40 normalises a refund name for the billing service.
func Refund40(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Batch41 normalises a batch name for the billing service.
func Batch41(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 29 {
		s = s[:29]
	}
	return strings.ToLower(s)
}

// Account42 normalises a account name for the billing service.
func Account42(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Header43 normalises a header name for the billing service.
func Header43(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Refund44 normalises a refund name for the billing service.
func Refund44(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Cursor45 normalises a cursor name for the billing service.
func Cursor45(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Queue46 normalises a queue name for the billing service.
func Queue46(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Tenant47 normalises a tenant name for the billing service.
func Tenant47(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 28 {
		s = s[:28]
	}
	return strings.ToLower(s)
}

// Mailbox48 normalises a mailbox name for the billing service.
func Mailbox48(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Ledger49 normalises a ledger name for the billing service.
func Ledger49(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Digest410 normalises a digest name for the billing service.
func Digest410(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Index411 normalises a index name for the billing service.
func Index411(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Order412 normalises a order name for the billing service.
func Order412(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Invoice413 normalises a invoice name for the billing service.
func Invoice413(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 5 {
		s = s[:5]
	}
	return strings.ToLower(s)
}
