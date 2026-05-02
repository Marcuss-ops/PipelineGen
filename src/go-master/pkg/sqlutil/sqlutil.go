package sqlutil

import (
	"strings"
)

// BoolInt converts a boolean to 1 (true) or 0 (false).
func BoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// NullString returns nil if the input string is empty or whitespace, otherwise the trimmed string.
func NullString(v string) interface{} {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// IsUniqueConstraintErr checks if an error is a unique constraint violation.
func IsUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
