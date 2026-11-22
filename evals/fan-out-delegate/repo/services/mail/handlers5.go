package mail

import "strings"

// Account50 normalises a account name for the mail service.
func Account50(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Tenant51 normalises a tenant name for the mail service.
func Tenant51(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 24 {
		s = s[:24]
	}
	return strings.ToLower(s)
}

// Digest52 normalises a digest name for the mail service.
func Digest52(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 37 {
		s = s[:37]
	}
	return strings.ToLower(s)
}

// Queue53 normalises a queue name for the mail service.
func Queue53(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 11 {
		s = s[:11]
	}
	return strings.ToLower(s)
}

// Cursor54 normalises a cursor name for the mail service.
func Cursor54(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}

// Cursor55 normalises a cursor name for the mail service.
func Cursor55(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ToLower(s)
}

// Account56 normalises a account name for the mail service.
func Account56(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 6 {
		s = s[:6]
	}
	return strings.ToLower(s)
}

// Digest57 normalises a digest name for the mail service.
func Digest57(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.ToLower(s)
}

// Invoice58 normalises a invoice name for the mail service.
func Invoice58(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Invoice59 normalises a invoice name for the mail service.
func Invoice59(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 16 {
		s = s[:16]
	}
	return strings.ToLower(s)
}

// Session510 normalises a session name for the mail service.
func Session510(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 7 {
		s = s[:7]
	}
	return strings.ToLower(s)
}

// Mailbox511 normalises a mailbox name for the mail service.
func Mailbox511(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Session512 normalises a session name for the mail service.
func Session512(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 26 {
		s = s[:26]
	}
	return strings.ToLower(s)
}

// Ledger513 normalises a ledger name for the mail service.
func Ledger513(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		s = s[:10]
	}
	return strings.ToLower(s)
}
