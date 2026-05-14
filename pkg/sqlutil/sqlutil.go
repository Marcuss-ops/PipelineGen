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

// BuildFallbackLikeConditions builds a set of LIKE conditions for a list of tokens across multiple columns.
// It enforces an AND relationship between different tokens (so all tokens must match),
// and an OR relationship between columns for a single token.
func BuildFallbackLikeConditions(tokens []string, columns []string) (string, []interface{}) {
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var andConditions []string
	var args []interface{}

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if len(token) < 2 {
			continue // Skip very short tokens to avoid overly broad LIKE %a% matches
		}

		var colConditions []string
		for _, col := range columns {
			colConditions = append(colConditions, col+" LIKE ?")
			args = append(args, "%"+token+"%")
		}

		andConditions = append(andConditions, "("+strings.Join(colConditions, " OR ")+")")
	}

	if len(andConditions) == 0 {
		return "", nil
	}

	return "(" + strings.Join(andConditions, " AND ") + ")", args
}
