// Package timeutil centralizes RFC3339 timestamp parsing/formatting and the
// "now" helper used across the codebase. It exists to:
//   - Eliminate ~33 duplicated `time.Parse(time.RFC3339, ...)` and
//     `t.Format(time.RFC3339)` call sites.
//   - Provide a single testable `Now()` for injection in unit tests.
//   - Handle nullable (*string / *time.Time) variants used by SQLite scans
//     and SQL parameter binding.
package timeutil

import "time"

// Now returns the current time in UTC. Centralizing this makes it possible
// to inject a clock in tests without monkey-patching time.Now.
func Now() time.Time {
	return time.Now().UTC()
}

// ParseRFC3339 parses an RFC3339 timestamp string into a time.Time.
// Returns the zero time.Time on empty input or parse error. Callers that
// need to distinguish "missing" from "present-but-invalid" should use
// ParseRFC3339PtrString on a *string instead.
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

// ParseRFC3339String is an alias for ParseRFC3339 kept for clarity at call
// sites that specifically deal with string-typed timestamps.
func ParseRFC3339String(s string) time.Time {
	return ParseRFC3339(s)
}

// ParseRFC3339PtrString parses a nullable string. Returns nil for nil or
// empty input; otherwise returns a *time.Time parsed via ParseRFC3339.
// A nil result is the canonical "not set" representation.
func ParseRFC3339PtrString(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t := ParseRFC3339(*s)
	if t.IsZero() {
		return nil
	}
	return &t
}

// FormatRFC3339 formats a time.Time as an RFC3339 string. Returns the empty
// string for the zero time so callers can persist a NULL-ish marker in
// TEXT columns without further checks.
func FormatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// FormatPtrRFC3339 formats a *time.Time for SQL parameter binding. Returns
// nil for a nil pointer so it can be passed directly to database/sql
// variadic args (which expect any / interface{}). Non-nil values are
// formatted as RFC3339 strings.
func FormatPtrRFC3339(t *time.Time) any {
	if t == nil {
		return nil
	}
	return FormatRFC3339(*t)
}

// ParseRFC3339Ptr parses a string and returns *time.Time. Returns nil for
// empty input or parse error. Useful for non-nullable time.Time fields
// that are scanned from nullable DB columns.
func ParseRFC3339Ptr(s string) *time.Time {
	t := ParseRFC3339(s)
	if t.IsZero() {
		return nil
	}
	return &t
}

// DerefString returns the value of a *string or "" if nil. Convenient for
// passing nullable string columns to helpers that take plain string.
func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ParseYouTubeUploadDate parses yt-dlp's upload_date YYYYMMDD format.
func ParseYouTubeUploadDate(dateStr string) (time.Time, error) {
	if len(dateStr) >= 8 {
		return time.Parse("20060102", dateStr[:8])
	}
	return time.Parse("20060102", dateStr)
}
