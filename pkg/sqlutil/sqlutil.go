// Package sqlutil provides SQL utility functions for building full-text
// search fallback conditions using LIKE (FTS5 is banned per project policy).
package sqlutil

import "strings"

// buildFallbackLikeConditions is the shared private helper backing
// both BuildFallbackLikeConditions (op="AND") and
// BuildFallbackLikeConditionsOR (op="OR"). op MUST be exactly "AND"
// or "OR" — the per-token column disjunct ("t1 LIKE ? OR t2 LIKE ? OR
// ...") is hard-wired because LIKE is column-scoped; only the
// across-token join operator varies.
//
// godlike/06 SSOT (one canonical owner per fact): this helper owns
// the FTS5-banned LIKE fallback construction. Single source of truth
// for token filtering (< 2 chars dropped) + per-token column
// disjunct + across-token join + outer paren-wrap. The two public
// functions are 1-line delegates with the join operator as the
// only difference.
//
// godlike/07 NO-FAKE-AVAILABILITY: the op parameter is intentionally
// a plain string (not a typed enum) BECAUSE the SQL join keyword is
// a string and a typed enum would mirror the same shape without
// runtime safety between bad op values + valid SQL. Misuse with an
// unsupported op surfaces as an invalid SQL fragment at query
// execution time (loud-failure) — not a silent no-op. The two
// production callers pass hardcoded literals so the misuse surface
// is reachable only via a future refactor that adds a third caller.
func buildFallbackLikeConditions(tokens []string, columns []string, op string) (string, []any) {
	if len(tokens) == 0 || len(columns) == 0 {
		return "", nil
	}

	var tokenConditions []string
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

		tokenConditions = append(tokenConditions, "("+strings.Join(colConditions, " OR ")+")")
	}

	if len(tokenConditions) == 0 {
		return "", nil
	}

	return "(" + strings.Join(tokenConditions, " "+op+" ") + ")", args
}

// BuildFallbackLikeConditions builds LIKE conditions with AND semantics
// across tokens (ALL tokens must match). Each token must be >= 2 chars.
func BuildFallbackLikeConditions(tokens []string, columns []string) (string, []any) {
	return buildFallbackLikeConditions(tokens, columns, "AND")
}

// BuildFallbackLikeConditionsOR builds LIKE conditions with OR semantics
// across keywords (ANY keyword can match). Useful as a broadening fallback
// when AND yields zero results.
func BuildFallbackLikeConditionsOR(tokens []string, columns []string) (string, []any) {
	return buildFallbackLikeConditions(tokens, columns, "OR")
}
