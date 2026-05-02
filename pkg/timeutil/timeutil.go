package timeutil

import "time"

// FormatRFC3339 formats a time to RFC3339 string.
func FormatRFC3339(t time.Time) string {
	return t.Format(time.RFC3339)
}

// FormatPtrRFC3339 formats a time pointer to RFC3339 string, returns nil if nil.
func FormatPtrRFC3339(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

// ParseRFC3339Ptr parses an RFC3339 string to a time pointer, returns nil if empty.
func ParseRFC3339Ptr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// ParseRFC3339 parses an RFC3339 string to time.Time, returns zero value if empty.
func ParseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ParseRFC3339PtrString parses an RFC3339 string pointer to a time pointer, returns nil if nil or empty.
func ParseRFC3339PtrString(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	return ParseRFC3339Ptr(*s)
}

// ParseRFC3339String parses an RFC3339 string pointer to time.Time, returns zero value if nil or empty.
func ParseRFC3339String(s *string) time.Time {
	if s == nil || *s == "" {
		return time.Time{}
	}
	return ParseRFC3339(*s)
}
