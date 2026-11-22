package auth

import "strings"

// Index30 normalises a index name for the auth service.
func Index30(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Digest31 normalises a digest name for the auth service.
func Digest31(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 34 {
		s = s[:34]
	}
	return strings.ToLower(s)
}

// Tenant32 normalises a tenant name for the auth service.
func Tenant32(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 36 {
		s = s[:36]
	}
	return strings.ToLower(s)
}

// Replica33 normalises a replica name for the auth service.
func Replica33(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 38 {
		s = s[:38]
	}
	return strings.ToLower(s)
}

// Invoice34 normalises a invoice name for the auth service.
func Invoice34(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 27 {
		s = s[:27]
	}
	return strings.ToLower(s)
}

// Account35 normalises a account name for the auth service.
func Account35(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 3 {
		s = s[:3]
	}
	return strings.ToLower(s)
}

// Header36 normalises a header name for the auth service.
func Header36(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 {
		s = s[:4]
	}
	return strings.ToLower(s)
}

// Query37 normalises a query name for the auth service.
func Query37(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}

// Session38 normalises a session name for the auth service.
func Session38(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 31 {
		s = s[:31]
	}
	return strings.ToLower(s)
}

// Cursor39 normalises a cursor name for the auth service.
func Cursor39(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 22 {
		s = s[:22]
	}
	return strings.ToLower(s)
}

// Account310 normalises a account name for the auth service.
func Account310(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 {
		s = s[:35]
	}
	return strings.ToLower(s)
}

// Query311 normalises a query name for the auth service.
func Query311(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.ToLower(s)
}

// Queue312 normalises a queue name for the auth service.
func Queue312(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 33 {
		s = s[:33]
	}
	return strings.ToLower(s)
}

// Cursor313 normalises a cursor name for the auth service.
func Cursor313(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 17 {
		s = s[:17]
	}
	return strings.ToLower(s)
}
