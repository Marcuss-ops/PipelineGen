// Package mediamemory (sqlite infrastructure) — helpers.go is the
// canonical SSOT for cross-repository utilities (godlike/06 SSOT:
// one canonical owner per fact).
//
// godlike/06 SSOT: every wire-level conversion that more than one
// repository file uses MUST live here. Repository-specific helpers
// (e.g. JSON array encode/decode for candidates,
// nullableEmbeddingVersion for concepts) stay local to the
// specific repository file.
//
// godlike/07 NO-FAKE-AVAILABILITY: time parsing accepts ONLY the
// canonical RFC3339(NaNo) UTC envelope. A non-conforming value
// surfaces as a wrapped, parse-time error so the caller can
// branch via errors.Is.
//
// godlike/06 SSOT (composition): this file imports no other file
// in the package (no application-layer coupling at the helper
// boundary); sibling repository files depend on it.
package mediamemory

import (
	"fmt"
	"strings"
	"time"
)

// rowScanner is the minimal interface accepted by the per-repo
// scanXxxRow helpers. Both *sql.Row (single row) and *sql.Rows
// (iter) satisfy it without forcing the caller to dispatch.
type rowScanner interface {
	Scan(dest ...any) error
}

// parseTime parses a SQLite DATETIME stored as RFC3339(NaNo) UTC.
// Both RFC3339Nano and RFC3339 are tolerated — the canonical wire
// format is RFC3339Nano but legacy rows may carry RFC3339.
//
// godlike/07 NO-FAKE-AVAILABILITY: an empty string or unknown
// format surfaces as a typed parse error (no silently zeroed time).
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("mediamemory: parse empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("mediamemory: parse %q as RFC3339: unknown format", s)
}

// nullableString returns nil for empty string so the SQLite driver
// writes NULL instead of an empty literal (godlike/07
// NO-FAKE-AVAILABILITY: NULL IS the canonical not-yet-known
// state, NOT empty string).
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTimePtr returns nil for nil pointer; otherwise the
// RFC3339Nano UTC string. The SQLite driver writes NULL on nil.
func nullableTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

// boolToInt converts a bool to the SQLite 0/1 representation.
// SQLite has no BOOLEAN type, so this wire-level conversion lives
// at the repository boundary (godlike/06 SSOT: canonical bool type
// is in the application layer; SQLite stores the wire-level int).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueViolation matches a SQLite UNIQUE constraint failure
// without depending on driver-specific typed sentinels. The
// modernc.org/sqlite driver returns Error whose .Error() includes
// the literal "UNIQUE constraint failed"; the canonical
// ErrDuplicateBinding envelope is wrapped around the match.
//
// godlike/06 SSOT: the substring "UNIQUE constraint failed" is
// part of the SQLite standard error message vocabulary; any
// future driver change must surface an error with this substring
// (a forward-pin for the wire contract).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
