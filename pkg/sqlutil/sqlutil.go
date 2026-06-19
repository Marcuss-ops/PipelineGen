// Package sqlutil provides SQL utility functions for building full-text
// search fallback conditions using LIKE (FTS5 is banned per project policy).
package sqlutil

import "strings"

// BuildFallbackLikeConditions builds LIKE conditions with AND semantics
// across tokens (ALL tokens must match). Each token must be >= 2 chars.
func BuildFallbackLikeConditions(tokens []string, columns []string) (string, []any) {
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var andConditions []string
	var args []any

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if len(token) < 2 {
			continue
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

// BuildFallbackLikeConditionsOR builds LIKE conditions with OR semantics
// across keywords (ANY keyword can match). Useful as a broadening fallback
// when AND yields zero results.
func BuildFallbackLikeConditionsOR(tokens []string, columns []string) (string, []any) {
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var orConditions []string
	var args []any

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if len(token) < 2 {
			continue
		}

		var colConditions []string
		for _, col := range columns {
			colConditions = append(colConditions, col+" LIKE ?")
			args = append(args, "%"+token+"%")
		}

		orConditions = append(orConditions, "("+strings.Join(colConditions, " OR ")+")")
	}

	if len(orConditions) == 0 {
		return "", nil
	}

	return "(" + strings.Join(orConditions, " OR ") + ")", args
}
