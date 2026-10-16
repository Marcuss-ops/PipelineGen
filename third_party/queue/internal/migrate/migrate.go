// Package migrate applies the queue schema migrations to PostgreSQL.
//
// Migrations are tracked in a schema_migrations table and applied
// transactionally, one file at a time, in lexicographic order. Apply is
// idempotent and safe to call on every startup; a Postgres advisory lock
// serializes concurrent migrators (multiple queue replicas, or parallel
// integration tests sharing a database).
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/queue/migrations"
)

// migrationLockKey is the advisory lock used to serialize migrations.
const migrationLockKey = 794237183 // arbitrary, unique to the RenderingGen queue

// sqlDB is the subset of *sql.DB / *sql.Conn used by the migrator.
type sqlDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// Apply runs all pending migrations against db. A migration is considered
// applied when its filename is recorded in schema_migrations.
func Apply(ctx context.Context, db *sql.DB) error {
	// Pin a single connection and hold a session-level advisory lock for the
	// whole run so concurrent migrators cannot race on CREATE TABLE.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	return apply(ctx, conn)
}

// apply runs the migration steps against a single pinned connection.
func apply(ctx context.Context, db sqlDB) error {
	if err := ensureLedger(ctx, db); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	names, err := migrations.Names()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := applyOne(ctx, db, name); err != nil {
			return err
		}
	}
	return nil
}

// ensureLedger creates the migration ledger table if it does not exist.
func ensureLedger(ctx context.Context, db sqlDB) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// appliedVersions returns the set of migration filenames already applied.
func appliedVersions(ctx context.Context, db sqlDB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyOne applies a single migration file inside a transaction and records it
// in the ledger only after the DDL succeeds.
func applyOne(ctx context.Context, db sqlDB, name string) error {
	script, err := migrations.FS.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()

	for _, stmt := range splitStatements(string(script)) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// splitStatements splits a SQL script into individual statements on semicolons
// that fall outside any construct where a semicolon is part of the text rather
// than a separator. It exists because PostgreSQL's extended query protocol does
// not accept multiple statements in a single Exec.
//
// The constructs it understands, and why each one is needed:
//
//   - line comments (-- ... to end of line), which are dropped from the
//     statement (the newline stays, so adjacent tokens never merge);
//   - block comments, which PostgreSQL NESTS: an inner open marker inside an
//     outer comment does not end it, so the scanner tracks a depth. They are
//     kept verbatim, because a comment can be the only separator between two
//     tokens;
//   - ordinary string literals, with the doubled-quote escape (” is a quote
//     inside the literal, not its end);
//   - escape string literals (E'...'), where a backslash also escapes the
//     closing quote;
//   - dollar-quoted strings ($$...$$ and $tag$...$tag$).
//
// The dollar-quote case is the one that makes this a real parser instead of a
// delimiter scan: a function body or a DO block is a single statement whose
// semicolons are all inside a dollar-quoted literal, and a naive split produced
// one statement per line of the body — which then failed at the server with a
// syntax error pointing at a line nobody wrote. Dollar quotes are also the
// reason a positional parameter ($1) must not be mistaken for a tag.
func splitStatements(script string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if stmt := strings.TrimSpace(b.String()); stmt != "" {
			out = append(out, stmt)
		}
		b.Reset()
	}

	for i := 0; i < len(script); {
		switch c := script[i]; {
		case c == '-' && i+1 < len(script) && script[i+1] == '-':
			// Line comment: a semicolon inside it is not a boundary.
			for i < len(script) && script[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(script) && script[i+1] == '*':
			i = copyBlockComment(&b, script, i)
		case c == '\'':
			i = copyStringLiteral(&b, script, i)
		case c == '$':
			tag, ok := dollarQuoteTag(script, i)
			if !ok {
				// A positional parameter ($1) or a bare dollar: ordinary text.
				b.WriteByte(c)
				i++
				continue
			}
			i = copyDollarQuoted(&b, script, i, tag)
		case c == ';':
			flush()
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// copyBlockComment copies a (possibly nested) block comment VERBATIM into the
// statement and returns the index just past it.
//
// It is copied rather than skipped because a comment can sit between two tokens
// where it is the only separator (`a /*x*/ b`): dropping it would silently join
// them into `a b` — or worse, into one token. An unterminated comment consumes
// the rest of the script, which the server rejects anyway; copying it keeps the
// splitter from inventing statements out of the comment's contents.
func copyBlockComment(b *strings.Builder, script string, start int) int {
	depth := 0
	i := start
	for i < len(script) {
		switch {
		case script[i] == '/' && i+1 < len(script) && script[i+1] == '*':
			depth++
			b.WriteString(script[i : i+2])
			i += 2
		case script[i] == '*' && i+1 < len(script) && script[i+1] == '/':
			depth--
			b.WriteString(script[i : i+2])
			i += 2
			if depth == 0 {
				return i
			}
		default:
			b.WriteByte(script[i])
			i++
		}
	}
	return i
}

// copyStringLiteral copies one '...' literal verbatim, honouring the doubled
// quote escape and, for an E'...' literal, the backslash escape. It returns the
// index just past the closing quote.
func copyStringLiteral(b *strings.Builder, script string, start int) int {
	escapes := isEscapeLiteral(script, start)
	i := start
	b.WriteByte(script[i])
	i++
	for i < len(script) {
		c := script[i]
		if escapes && c == '\\' && i+1 < len(script) {
			b.WriteByte(c)
			b.WriteByte(script[i+1])
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
		if c == '\'' {
			// '' inside the literal is an escaped quote, not the end.
			if i < len(script) && script[i] == '\'' {
				b.WriteByte(script[i])
				i++
				continue
			}
			return i
		}
	}
	return i
}

// isEscapeLiteral reports whether the quote at start opens an E'...' literal, in
// which a backslash escapes the closing quote.
func isEscapeLiteral(script string, start int) bool {
	if start == 0 {
		return false
	}
	prev := script[start-1]
	if prev != 'E' && prev != 'e' {
		return false
	}
	// The E must be a standalone prefix: `NAME'` is not an escape literal.
	return start-1 == 0 || !isIdentChar(script[start-2])
}

// dollarQuoteTag returns the opening dollar-quote tag at start ("$$", "$body$",
// ...). It reports false for a positional parameter such as $1, which has no
// closing dollar.
func dollarQuoteTag(script string, start int) (string, bool) {
	i := start + 1
	for i < len(script) && isIdentChar(script[i]) {
		i++
	}
	if i < len(script) && script[i] == '$' {
		return script[start : i+1], true
	}
	return "", false
}

// copyDollarQuoted copies a dollar-quoted body verbatim through its own closing
// tag, so every semicolon inside it stays part of the same statement.
func copyDollarQuoted(b *strings.Builder, script string, start int, tag string) int {
	bodyStart := start + len(tag)
	end := strings.Index(script[bodyStart:], tag)
	if end < 0 {
		// Unterminated: keep the remainder as one statement body rather than
		// splitting it into pieces the server never saw.
		b.WriteString(script[start:])
		return len(script)
	}
	stop := bodyStart + end + len(tag)
	b.WriteString(script[start:stop])
	return stop
}

// isIdentChar reports whether c can appear in a dollar-quote tag.
func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
