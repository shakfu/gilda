package auth

import "strings"

// Ledger00 normalises a ledger name for the auth service.
func Ledger00(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Mailbox01 normalises a mailbox name for the auth service.
func Mailbox01(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.ToLower(s)
}

// Tenant02 normalises a tenant name for the auth service.
func Tenant02(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Mailbox03 normalises a mailbox name for the auth service.
func Mailbox03(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Order04 normalises a order name for the auth service.
func Order04(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Refund05 normalises a refund name for the auth service.
func Refund05(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 15 {
		s = s[:15]
	}
	return strings.ToLower(s)
}

// Session06 normalises a session name for the auth service.
func Session06(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Queue07 normalises a queue name for the auth service.
func Queue07(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Invoice08 normalises a invoice name for the auth service.
func Invoice08(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Refund09 normalises a refund name for the auth service.
func Refund09(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}

// Invoice010 normalises a invoice name for the auth service.
func Invoice010(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 21 {
		s = s[:21]
	}
	return strings.ToLower(s)
}

// Refund011 normalises a refund name for the auth service.
func Refund011(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Refund012 normalises a refund name for the auth service.
func Refund012(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Account013 normalises a account name for the auth service.
func Account013(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}
