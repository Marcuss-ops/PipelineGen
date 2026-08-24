package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestMigrations_SchemaContract_AllTables is the catch-all schema-contract
// test that locks in the invariant the user asked for:
//
//	"Aggiungi test che fa PRAGMA table_info per ogni migration CREATE TABLE
//	 e lo confronta con sqlite_master — ogni migration deve dichiarare lo
//	 schema che si aspetta"
//
// For every migration file under migrations/sqlite/ that:
//   - declares a CREATE TABLE [IF NOT EXISTS] statement, OR
//   - declares an ALTER TABLE … ADD COLUMN statement
//
// we parse the declared column list (name + raw type) and assert that
// the live sqlite_master schema, after a clean `RunMigrationsOnDB` apply,
// contains exactly those columns in the expected order:
//
//   - The CREATE TABLE columns appear in their declared order at the
//     BEGINNING of the live table (SQLite preserves the original column
//     list verbatim across ALTERs that append to it).
//   - ALTER ADD COLUMNs appear in their declared order at the END of the
//     live table (SQLite's ALTER TABLE ADD COLUMN always appends).
//   - The full column count = CREATE count + ALTER count.
//
// Scope-aware: migrations 109 + 128 carry `-- database: primary` and are
// skipped on the observability DB. Scope=all migrations (most files,
// including 092 + 093) apply to both targets — the test asserts parity.
//
// Files that contain only a no-op `SELECT 1;` (091) or zero DDL
// statements (003) contribute zero expectations and are silently skipped.
func TestMigrations_SchemaContract_AllTables(t *testing.T) {
	const migrationsRelPath = "../../../migrations/sqlite"
	migrationsDir, err := filepath.Abs(migrationsRelPath)
	require.NoError(t, err, "resolve migrations dir")
	migrationsDir = filepath.Clean(migrationsDir)

	for _, targetDB := range []string{"primary", "observability"} {
		targetDB := targetDB
		t.Run("scope="+targetDB, func(t *testing.T) {
			expected := extractExpectedSchema(t, migrationsDir, targetDB)
			require.NotEmpty(t, expected,
				"parser must yield at least one expected table for scope=%s — "+
					"if this fires, the migration dir is empty or the parser is broken",
				targetDB)

			// Apply all in-scope migrations on a fresh DB. Use WAL + FK +
			// 5s busy_timeout for parity with production.
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "contract.sqlite")

			log := zaptest.NewLogger(t)
			err := RunMigrationsOnDB(dbPath, log, migrationsDir, targetDB)
			require.NoError(t, err, "RunMigrationsOnDB must succeed on fresh DB for scope=%s", targetDB)

			db, err := sql.Open("sqlite3", dbPath+"?_mode=ro")
			require.NoError(t, err)
			defer db.Close()

			// For each expected table, verify the contract.
			names := make([]string, 0, len(expected))
			for k := range expected {
				names = append(names, k)
			}
			sort.Strings(names)

			for _, tbl := range names {
				// Bind per-iteration values for the t.Run closure. The
				// "tbl := tbl" / "exp := exp" idiom is the canonical Go
				// pattern for capturing loop variables by value when the
				// closure outlives the loop iteration (e.g. parallel
				// t.Run). It's not strictly needed here because t.Run
				// is synchronous, but it documents the intent and is
				// defensive against a future refactor that switches to
				// t.Parallel.
				tbl := tbl
				exp := expected[tbl]
				if exp.DroppedInFile != "" {
					// Table was created by an earlier migration and
					// then dropped by `exp.DroppedInFile` — the fresh-DB
					// schema correctly does NOT contain it. Skip the
					// contract check; the dropping migration is the
					// authoritative declaration that the table's
					// lifecycle ended.
					t.Run("table="+tbl+"_dropped_by_"+exp.DroppedInFile, func(t *testing.T) {
						t.Skipf("table %q was correctly dropped by %s; no contract check needed",
							tbl, exp.DroppedInFile)
					})
					continue
				}
				t.Run("table="+tbl, func(t *testing.T) {
					assertTableContract(t, db, tbl, exp)
				})
			}
		})
	}
}

// assertTableContract verifies that `tbl` exists in sqlite_master and that
// its PRAGMA table_info matches the migration-declared schema for `exp`.
// See the file-level comment for the column-order rule.
func assertTableContract(t *testing.T, db *sql.DB, tbl string, exp *expectedTable) {
	t.Helper()

	// 1. Table must exist.
	var tableSQL string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`,
		tbl,
	).Scan(&tableSQL)
	if err == sql.ErrNoRows {
		t.Fatalf("table %q declared by %q does NOT exist in sqlite_master after fresh-DB apply. "+
			"Migration declares CREATE TABLE but the runner did not produce it. "+
			"Check (1) the file is in-scope for the target DB, (2) the CREATE TABLE statement parsed, "+
			"(3) RunMigrationsOnDB applied it (look for `applying migration` log line).",
			tbl, exp.CreateFile)
	}
	require.NoError(t, err, "sqlite_master lookup for %q", tbl)

	// 2. Read the live column list.
	live, err := readPRAGMATableInfo(t, db, tbl)
	require.NoError(t, err, "PRAGMA table_info(%s)", tbl)

	// 3. Reconstruct the expected post-migrate column list:
	//    CREATE columns in declared order, then ALTER columns in declared
	//    (file) order. SQLite's ALTER TABLE ADD COLUMN always appends.
	var expectedCols []expectedColumn
	expectedCols = append(expectedCols, exp.CreateCols...)
	expectedCols = append(expectedCols, exp.AddedCols...)

	require.Equal(t,
		len(expectedCols), len(live),
		"column count mismatch for %q: migration declared %d columns (%d CREATE + %d ALTER), "+
			"but live schema has %d columns. CREATE cols: %v, ALTER cols: %v, live: %v",
		tbl,
		len(expectedCols), len(exp.CreateCols), len(exp.AddedCols), len(live),
		colNames(exp.CreateCols), colNames(exp.AddedCols), liveNames(live),
	)

	// 4. Per-column: name + raw type at each ordinal must match.
	for i, want := range expectedCols {
		got := live[i]
		require.Equal(t, want.Name, got.Name,
			"column name drift at ordinal %d in table %q: migration declared %q (from %q) "+
				"but PRAGMA table_info reports %q",
			i, tbl, want.Name, want.Origin, got.Name)
		// Raw type comparison: PRAGMA returns the exact literal type name
		// the migration used. A mismatch means the migration declares
		// one type and SQLite stored another (e.g. via canonical.go SSOT
		// aliasing TEXT → VARCHAR — that drift is exactly what this
		// test exists to catch).
		require.Equal(t, want.Type, got.Type,
			"column type drift at ordinal %d in table %q: migration declared %q (%s) but "+
				"PRAGMA table_info reports type %q. Raw type strings should match — if one side "+
				"says TEXT and the other VARCHAR, fix the migration to match the canonical schema "+
				"or fix the canonical schema to match the migration.",
			i, tbl, want.Name, want.Origin, got.Type)
	}
}

// expectedTable is the per-table expected schema built up by
// extractExpectedSchema. CreateCols is the ordered column list from the
// CREATE TABLE statement that first introduced the table. AddedCols is
// the ordered list of columns added via ALTER TABLE ADD COLUMN in
// subsequent files (file-order preserved). DroppedInFile is non-empty
// when a later migration in the same scope drops this table (via DROP
// TABLE or via RENAME TO a different name) — the assertion loop skips
// the contract check for such tables. RenamedTo is set when the
// migration is `ALTER TABLE oldname RENAME TO newname` and surfaces
// the canonical post-rename name for operator-visible diagnostics.
type expectedTable struct {
	Name          string
	CreateFile    string // migration file that originally created the table
	CreateCols    []expectedColumn
	AddedCols     []expectedColumn
	DroppedInFile string // empty unless a later migration drops this table
	RenamedTo     string // empty unless a later migration RENAME TO this name
}

type expectedColumn struct {
	Name   string
	Type   string // raw type name, e.g. "TEXT", "INTEGER", "REAL", "BLOB"
	Origin string // migration file that declared this column
}

// liveColumn is the row shape returned by PRAGMA table_info.
type liveColumn struct {
	cid       int
	Name      string
	Type      string
	notnull   int
	dfltValue sql.NullString
	pk        int
}

// extractExpectedSchema parses every migration file in migrationsDir
// (in lexicographic order, matching RunMigrations' discovery order) and
// returns the accumulated expected schema for the given targetDB.
//
// The parser:
//   - Respects the `-- database:` scope header (skips primary-only files
//     when targetDB="observability").
//   - Extracts CREATE TABLE column lists with a parenthesis-counting
//     splitter so DEFAULT (datetime('now')) and similar nested parens
//     don't break the body split.
//   - Extracts ALTER TABLE ADD COLUMN entries.
//   - Skips table-level constraints (PRIMARY KEY (cols), FOREIGN KEY,
//     UNIQUE (cols), CHECK, CONSTRAINT) at the top of a column-list
//     segment, since those are not columns.
//
// Files with zero DDL (e.g. 003_add_embeddings.sql, 091_*.sql) naturally
// contribute zero expectations.
func extractExpectedSchema(t *testing.T, migrationsDir, targetDB string) map[string]*expectedTable {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	require.NoError(t, err, "glob migrations dir")
	sort.Strings(files) // RunMigrations runs in version order (lex on NNN_*)

	schema := make(map[string]*expectedTable)

	for _, path := range files {
		filename := filepath.Base(path)
		contentBytes, err := os.ReadFile(path)
		require.NoError(t, err, "read %s", filename)
		content := string(contentBytes)

		// Scope filter: parse the `-- database:` header (default "all")
		// and skip the file if it doesn't apply to the targetDB.
		// parseMigrationScope takes []byte (matches the runner's internal
		// convention; the file is read as []byte everywhere in
		// migrations.go for SHA-256 hashing).
		scope := parseMigrationScope(contentBytes)
		if !migrationAppliesToTargetDB(scope, targetDB) {
			continue
		}

		// Strip SQL line comments (-- to end of line) before parsing
		// the body. SQLite supports both -- and /* */ but the
		// migrations in this repo only use --.
		stripped := stripLineComments(content)

		// 1. CREATE TABLE column lists.
		for _, match := range createTablePattern.FindAllStringSubmatch(stripped, -1) {
			tbl := match[1]
			cols := parseColumnList(match[2])
			exp, ok := schema[tbl]
			if !ok {
				schema[tbl] = &expectedTable{
					Name:       tbl,
					CreateFile: filename,
					CreateCols: cols,
				}
				continue
			}
			// Same table re-declared by a later migration (rare; e.g.
			// jobs_new created in 053, re-declared in 069). Merge
			// any new column names we haven't seen yet.
			exp.CreateCols = mergeNewColumns(exp.CreateCols, cols)
		}

		// 2. ALTER TABLE ADD COLUMN entries.
		for _, match := range alterAddColumnPattern.FindAllStringSubmatch(stripped, -1) {
			tbl := match[1]
			colName := match[2]
			// Type is the rest of the line up to end of statement (semicolon)
			// or end of input. We strip the trailing semicolon + ws and
			// keep the raw type-affinity token (first word).
			colType := extractFirstTypeToken(match[3])
			exp, ok := schema[tbl]
			if !ok {
				// ALTER on a table that no migration CREATEd in scope.
				// Shouldn't happen in well-formed repos, but be defensive.
				exp = &expectedTable{Name: tbl}
				schema[tbl] = exp
			}
			if !hasExpectedColumn(exp, colName) {
				exp.AddedCols = append(exp.AddedCols, expectedColumn{
					Name:   colName,
					Type:   colType,
					Origin: filename,
				})
			}
		}

		// 3. DROP TABLE [IF EXISTS] entries. When a migration drops a
		//    table that an earlier migration created (e.g. 001 creates
		//    media_files, 010 drops it as part of the deprecation
		//    cleanup), the table is correctly absent from the post-migrate
		//    fresh-DB schema. Mark it as dropped so the assertion loop
		//    skips it (the test would otherwise report a false-positive
		//    "table declared by 001 does NOT exist in sqlite_master"
		//    failure).
		for _, match := range dropTablePattern.FindAllStringSubmatch(stripped, -1) {
			tbl := match[1]
			if exp, ok := schema[tbl]; ok {
				exp.DroppedInFile = filename
			} else {
				// DROP on a table that no in-scope migration CREATEd.
				// Could be a `DROP TABLE IF EXISTS` for a no-op cleanup
				// migration; track it for completeness.
				schema[tbl] = &expectedTable{Name: tbl, DroppedInFile: filename}
			}
		}

		// 4. ALTER TABLE … RENAME TO entries. When a migration renames
		//    a table (e.g. 053 creates jobs_new, 069 drops jobs and
		//    renames jobs_new to jobs; 114 creates youtube_discoveries_v2,
		//    drops youtube_discoveries, and renames v2 to youtube_discoveries),
		//    the source name is correctly absent from the post-migrate
		//    fresh-DB schema. Mark the old name as renamed so the
		//    assertion loop skips it. The new name is the canonical
		//    identifier for any later ALTER/INDEX statements.
		for _, match := range renameTablePattern.FindAllStringSubmatch(stripped, -1) {
			oldName := match[1]
			newName := match[2]
			if exp, ok := schema[oldName]; ok {
				exp.DroppedInFile = filename // Re-use: "absent post-migrate" semantics
				exp.RenamedTo = newName
			}
		}

		// 5. ALTER TABLE … DROP COLUMN entries. A migration that drops
		//    a column (e.g. 101 drops media_assets.status because
		//    lifecycle_state is the new SSOT) makes that column
		//    correctly absent from the post-migrate fresh-DB schema.
		//    Filter the dropped column out of CreateCols/AddedCols so
		//    the assertion loop doesn't surface a false-positive
		//    "column drift" failure.
		//
		//    Note: SQLite's ALTER TABLE DROP COLUMN requires SQLite
		//    >= 3.35.0 (March 2021). The bundled mattn/go-sqlite3
		//    driver is well past that version.
		for _, match := range dropColumnPattern.FindAllStringSubmatch(stripped, -1) {
			tbl := match[1]
			colName := match[2]
			exp, ok := schema[tbl]
			if !ok {
				continue
			}
			exp.CreateCols = removeExpectedColumn(exp.CreateCols, colName)
			exp.AddedCols = removeExpectedColumn(exp.AddedCols, colName)
		}
	}

	return schema
}

// Patterns used to extract DDL. Note: case-insensitive, multi-line aware.
var (
	// CREATE TABLE name ( cols );
	createTablePattern = regexp.MustCompile(
		`(?is)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.*?)\)\s*;`,
	)
	// ALTER TABLE name ADD [COLUMN] colName type [...]
	alterAddColumnPattern = regexp.MustCompile(
		`(?i)\bALTER\s+TABLE\s+` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s+ADD\s+(?:COLUMN\s+)?` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s+([^\n;]+)`,
	)
	// DROP TABLE [IF EXISTS] name;
	dropTablePattern = regexp.MustCompile(
		`(?i)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s*;`,
	)
	// ALTER TABLE oldname RENAME TO newname;
	renameTablePattern = regexp.MustCompile(
		`(?i)\bALTER\s+TABLE\s+` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s+RENAME\s+TO\s+` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s*;`,
	)
	// ALTER TABLE name DROP [COLUMN] colName;
	// Requires SQLite >= 3.35.0 (bundled driver is well past that).
	dropColumnPattern = regexp.MustCompile(
		`(?i)\bALTER\s+TABLE\s+` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s+DROP\s+(?:COLUMN\s+)?` +
			`([a-zA-Z_][a-zA-Z0-9_]*)\s*;`,
	)
)

// parseColumnList splits a CREATE TABLE body into per-column definitions
// using a parenthesis-counting splitter so DEFAULT (datetime('now')) and
// CHECK (a > b) keep their commas inside the default expression.
func parseColumnList(body string) []expectedColumn {
	var segments []string
	depth := 0
	start := 0
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				segments = append(segments, body[start:i])
				start = i + 1
			}
		}
	}
	// Tail segment (no trailing comma).
	if start < len(body) {
		segments = append(segments, body[start:])
	}

	var cols []expectedColumn
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		// Skip table-level constraints. The match must be a WHOLE WORD
		// (followed by whitespace or '(') so column names like
		// `check_interval`, `unique_id`, or `primary_key_id` are NOT
		// misclassified as constraints. `strings.HasPrefix(upper,
		// "CHECK")` would incorrectly filter such columns because
		// `strings.HasPrefix("CHECK_INTERVAL TEXT...", "CHECK") == true`.
		upper := strings.ToUpper(seg)
		if isTableLevelConstraint(upper) {
			continue
		}
		// Column definition: <name> <type> [constraints...]
		fields := strings.Fields(seg)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		// Strip quoting if any (e.g. "name" → name).
		name = strings.Trim(name, `"`)
		cols = append(cols, expectedColumn{
			Name: name,
			Type: strings.ToUpper(fields[1]),
		})
	}
	return cols
}

// isTableLevelConstraint reports whether an upper-cased CREATE TABLE body
// segment is a table-level constraint (PRIMARY KEY, FOREIGN KEY, UNIQUE,
// CHECK, CONSTRAINT) rather than a column definition. The match is
// whole-word: keyword followed by whitespace or '(' so column names
// like `check_interval` are not misclassified.
//
// Exposed at package level (not just inside parseColumnList) so the
// diagnostic tool / future parser extensions can reuse it.
func isTableLevelConstraint(upperSegment string) bool {
	for _, kw := range []string{"PRIMARY KEY", "FOREIGN KEY", "UNIQUE", "CHECK", "CONSTRAINT"} {
		if len(upperSegment) <= len(kw) {
			continue
		}
		if !strings.HasPrefix(upperSegment, kw) {
			continue
		}
		// The character immediately after the keyword must be whitespace
		// or '(' — i.e. the keyword is a complete word, not the prefix
		// of a longer identifier like CHECK_INTERVAL.
		next := upperSegment[len(kw)]
		if next == ' ' || next == '\t' || next == '(' {
			return true
		}
	}
	return false
}

// extractFirstTypeToken pulls the first whitespace-separated token from
// an ALTER ADD COLUMN type-and-constraints tail. The raw type is
// usually the first token (e.g. "TEXT", "INTEGER", "REAL"); trailing
// tokens are NOT NULL, DEFAULT, etc.
func extractFirstTypeToken(tail string) string {
	// Strip trailing semicolons / whitespace.
	tail = strings.TrimRight(tail, "; \t")
	fields := strings.Fields(tail)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// mergeNewColumns appends any columns from `incoming` not already in
// `existing`, preserving each slice's order. Used when the same table
// is CREATEd by more than one migration file (rare but possible).
func mergeNewColumns(existing, incoming []expectedColumn) []expectedColumn {
	seen := make(map[string]struct{}, len(existing))
	for _, c := range existing {
		seen[c.Name] = struct{}{}
	}
	for _, c := range incoming {
		if _, ok := seen[c.Name]; ok {
			continue
		}
		seen[c.Name] = struct{}{}
		existing = append(existing, c)
	}
	return existing
}

// hasExpectedColumn reports whether `name` is already declared in either
// CreateCols or AddedCols of `exp`.
func hasExpectedColumn(exp *expectedTable, name string) bool {
	for _, c := range exp.CreateCols {
		if c.Name == name {
			return true
		}
	}
	for _, c := range exp.AddedCols {
		if c.Name == name {
			return true
		}
	}
	return false
}

// removeExpectedColumn returns a fresh slice with any column whose Name
// matches `name` filtered out. Used by the ALTER TABLE DROP COLUMN path
// to keep the expected schema in sync with what the post-migrate
// fresh-DB schema will actually contain.
func removeExpectedColumn(cols []expectedColumn, name string) []expectedColumn {
	out := make([]expectedColumn, 0, len(cols))
	for _, c := range cols {
		if c.Name == name {
			continue
		}
		out = append(out, c)
	}
	return out
}

// stripLineComments removes `--` to EOL comments. Multi-line /* */ are
// not used in this repo's migrations, so we don't handle them.
func stripLineComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// readPRAGMATableInfo returns the live column list of `tbl` via
// `PRAGMA table_info`. Sorted by `cid` (declaration order).
func readPRAGMATableInfo(t *testing.T, db *sql.DB, tbl string) ([]liveColumn, error) {
	t.Helper()
	rows, err := db.Query("SELECT cid, name, type, \"notnull\", dflt_value, pk FROM pragma_table_info(?)", tbl)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []liveColumn
	for rows.Next() {
		var lc liveColumn
		if err := rows.Scan(&lc.cid, &lc.Name, &lc.Type, &lc.notnull, &lc.dfltValue, &lc.pk); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// colNames is a small helper for diff error messages.
func colNames(cols []expectedColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = fmt.Sprintf("%s:%s", c.Name, c.Type)
	}
	return out
}

// liveNames returns the names of `live` for diff error messages.
func liveNames(live []liveColumn) []string {
	out := make([]string, len(live))
	for i, lc := range live {
		out[i] = fmt.Sprintf("%s:%s", lc.Name, lc.Type)
	}
	return out
}
