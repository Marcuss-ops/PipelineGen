package storage

import (
	"strings"
)

// splitSQLStatements splits a migration file body into individual SQL
// statements. A statement boundary is a `;` that occurs OUTSIDE:
//   - string literals ('...' and "...")
//   - BEGIN...END trigger/function bodies (depth-tracked by keyword)
//
// Line comments (`-- ...`) are stripped in a pre-pass so semicolons inside
// comments do not produce false boundaries.
//
// This correctly handles migrations/sqlite/034_media_index_outbox.sql
// which contains a CREATE TRIGGER with an embedded `;` inside its
// BEGIN...END body. The naive line-based splitter was flushing at the
// inner `;` and producing a partial CREATE TRIGGER statement that failed
// with a syntax error.
//
// Caveat: matches BEGIN/END case-insensitively as whole words. None of
// our 47 migrations use BEGIN TRANSACTION/ COMMIT pairs, so the depth model
// holds — every BEGIN pairs with a matching END on the trigger body.
func splitSQLStatements(body string) []string {
	// Pre-pass: strip `--` line comments so semicolons inside comments
	// can't confuse the splitter.
	var buf strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	body = buf.String()

	var (
		statements []string
		current    strings.Builder
		inString   bool // tracks single-quoted SQL string literals
		beginDepth int  // tracks BEGIN ... END nesting
		caseDepth  int  // tracks CASE ... END nesting inside trigger bodies
	)
	flush := func() {
		stmt := strings.TrimSpace(current.String())
		if stmt != "" {
			statements = append(statements, stmt)
		}
		current.Reset()
	}

	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inString {
			current.WriteRune(c)
			if c == '\'' {
				// SQL-standard escaped quote ('')
				if i+1 < len(runes) && runes[i+1] == '\'' {
					current.WriteRune(runes[i+1])
					i++
				} else {
					inString = false
				}
			}
			continue
		}

		if c == '\'' {
			inString = true
			current.WriteRune(c)
			continue
		}

		// Word-character run: detect BEGIN/END keywords for depth tracking.
		if isAlphaWordRune(c) {
			j := i
			for j < len(runes) && isAlphaWordRune(runes[j]) {
				j++
			}
			word := strings.ToUpper(string(runes[i:j]))
			switch word {
			case "CASE":
				caseDepth++
			case "BEGIN":
				// Peek past whitespace at j: if the next word is one of SQLite's
				// transaction modifiers (IMMEDIATE/TRANSACTION/EXCLUSIVE/DEFERRED),
				// this BEGIN is a transaction-starter and must NOT increment depth.
				// Otherwise, it opens a CREATE TRIGGER body and depth goes up.
				if isTransactionModifierAfter(runes, j) {
					// leave beginDepth unchanged; pre-flight skip will catch
					// the resulting standalone `BEGIN IMMEDIATE` etc.
				} else {
					beginDepth++
				}
			case "END":
				if caseDepth > 0 {
					caseDepth--
				} else if beginDepth > 0 {
					beginDepth--
				}
			}
			for k := i; k < j; k++ {
				current.WriteRune(runes[k])
			}
			i = j - 1 // outer for-loop advances by 1
			continue
		}

		// Statement boundary: `;` outside any BEGIN...END and outside strings.
		if c == ';' && beginDepth == 0 && caseDepth == 0 {
			current.WriteRune(c)
			flush()
			continue
		}

		current.WriteRune(c)
	}

	if rest := strings.TrimSpace(current.String()); rest != "" {
		statements = append(statements, rest)
	}
	return statements
}

// isAlphaWordRune reports whether r is part of a SQL identifier character.
// Used for BEGIN/END keyword detection at word boundaries.
func isAlphaWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// isTransactionModifierAfter reports whether the rune at `after` (immediately
// following the read of a BEGIN keyword) is one of SQLite's transaction
// modifiers (IMMEDIATE / TRANSACTION / EXCLUSIVE / DEFERRED), which makes
// the BEGIN a transaction-starter rather than a CREATE TRIGGER body opener.
// Whitespace between BEGIN and the modifier is skipped.
func isTransactionModifierAfter(runes []rune, after int) bool {
	k := after
	for k < len(runes) && (runes[k] == ' ' || runes[k] == '\t' || runes[k] == '\n' || runes[k] == '\r') {
		k++
	}
	if k >= len(runes) || !isAlphaWordRune(runes[k]) {
		return false
	}
	m := k
	for m < len(runes) && isAlphaWordRune(runes[m]) {
		m++
	}
	switch strings.ToUpper(string(runes[k:m])) {
	case "IMMEDIATE", "TRANSACTION", "EXCLUSIVE", "DEFERRED":
		return true
	}
	return false
}

// isDuplicateColumnError reports whether errMsg is SQLite's
// "duplicate column name" error AND the offending stmt is an
// `ALTER TABLE ... ADD COLUMN` statement.
//
// The ALTER TABLE … ADD COLUMN scoping prevents the runner from silently
// swallowing unrelated "duplicate column name" errors that could arise
// from other DDL (e.g. a CREATE TABLE statement with a duplicated column
// in its column list — a real bug, not an idempotency retry). Only the
// ADD-COLUMN case we want to handle is soft-skipped.
func isDuplicateColumnError(errMsg, stmt string) bool {
	if !strings.Contains(errMsg, "duplicate column name") {
		return false
	}
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	return strings.HasPrefix(upper, "ALTER TABLE") &&
		strings.Contains(upper, "ADD COLUMN")
}

// isConditionalInsertOnMissingTable reports whether errMsg is SQLite's
// "no such table" error AND the offending stmt is a conditional data
// migration shaped like
//
//	INSERT [OR IGNORE] INTO <dst> SELECT ... FROM <src>
//	WHERE EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='<src>')
//
// SQLite's query planner resolves the FROM-table at planning time even
// when the EXISTS gate would have made the row source empty, so on a
// fresh DB where <src> was dropped (or never existed because of a
// refactor), the INSERT errors out despite the author's intent that it
// should be a no-op. We soft-skip this specific shape only — anything
// else that errors with "no such table" still hard-fails.
func isConditionalInsertOnMissingTable(errMsg, stmt string) bool {
	if !strings.Contains(errMsg, "no such table") {
		return false
	}
	upper := strings.ToUpper(stmt)
	// Match the gate the migration author wrote: an EXISTS check on
	// sqlite_master that names the missing table.
	return strings.Contains(upper, "WHERE EXISTS (SELECT 1 FROM SQLITE_MASTER WHERE TYPE='TABLE'")
}

// isNestedTransactionControl reports whether the statement is exactly a
// standalone SQLite transaction-control command (BEGIN, BEGIN TRANSACTION,
// BEGIN IMMEDIATE, BEGIN EXCLUSIVE, BEGIN DEFERRED, COMMIT, END, END
// TRANSACTION, ROLLBACK).
//
// These appear inside migration files the author wrote expecting to need
// explicit tx boundaries, but drive.RunMigrations already wraps each
// migration in an outer transaction, so nested BEGIN/COMMIT consistently
// errors with "cannot start a transaction within a transaction".
//
// `END` is recognised as a transaction-commit synonym (per SQLite docs);
// a bare `END` inside a CREATE TRIGGER body is filtered out before this
// check runs because splitSQLStatements' BEGIN/END depth tracking emits
// the whole trigger body as one statement, never a standalone bare `END`.
//
// The check is exact-string-match after trim and uppercase, so:
//   - mid-expression occurrences (e.g. column names, comments containing
//     the word "BEGIN") do NOT match;
//   - splitSQLStatements already guarantees these appear as standalone
//     statements (no trailing `;` after trim+strip).
func isNestedTransactionControl(stmt string) bool {
	s := strings.ToUpper(strings.TrimSpace(stmt))
	s = strings.TrimRight(s, ";")
	s = strings.TrimSpace(s)
	switch s {
	case "BEGIN", "BEGIN TRANSACTION", "BEGIN IMMEDIATE", "BEGIN EXCLUSIVE", "BEGIN DEFERRED",
		"COMMIT", "END", "END TRANSACTION", "ROLLBACK":
		return true
	}
	return false
}
