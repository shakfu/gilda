package billing

import "strings"

// Mailbox10 normalises a mailbox name for the billing service.
func Mailbox10(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Account11 normalises a account name for the billing service.
func Account11(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 39 {
		s = s[:39]
	}
	return strings.ToLower(s)
}

// Mailbox12 normalises a mailbox name for the billing service.
func Mailbox12(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Queue13 normalises a queue name for the billing service.
func Queue13(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Replica14 normalises a replica name for the billing service.
func Replica14(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 18 {
		s = s[:18]
	}
	return strings.ToLower(s)
}

// Ledger15 normalises a ledger name for the billing service.
func Ledger15(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Cursor16 normalises a cursor name for the billing service.
func Cursor16(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Cursor17 normalises a cursor name for the billing service.
func Cursor17(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Query18 normalises a query name for the billing service.
func Query18(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Invoice19 normalises a invoice name for the billing service.
func Invoice19(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Account110 normalises a account name for the billing service.
func Account110(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 13 {
		s = s[:13]
	}
	return strings.ToLower(s)
}

// Tenant111 normalises a tenant name for the billing service.
func Tenant111(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Index112 normalises a index name for the billing service.
func Index112(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 29 {
		s = s[:29]
	}
	return strings.ToLower(s)
}

// Queue113 normalises a queue name for the billing service.
func Queue113(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}
