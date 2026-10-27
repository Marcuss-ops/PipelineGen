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

// TestMigrations_TwinSchemaParity is the executable shared DDL source for the
// migration museum's twin declarations (migration-museum audit point 7; see
// docs/migrations/BASELINE_INVENTORY.md "Twin declarations").
//
// Nine table families are declared twice by hand — canonical on the primary
// plane (migrations/sqlite) and as a self-contained jobs-plane copy
// (migrations/sqlite_jobs/001_jobs_plane.sql) — and outbox_events is declared
// a third time on the PostgreSQL media SSOT (migrations/postgres/001_media_schema.sql).
// There is no generated DDL source: the copies are hand rewrites, and hand
// rewrites drift. The recorded instance is the job_checkpoints disappearance
// of BASELINE_INVENTORY.md ("Post-baseline drift repair (272)"); the silent
// divergence found by this audit is the outbox_events.event_key unique index
// (full on the primary copy, partial everywhere else — repaired by 285).
//
// This test applies both planes to fresh databases, reads the LIVE schemas the
// writers actually see, and requires them to agree except for the pinned
// divergences below — each one a deliberate, rationale'd exception. Any NEW
// divergence fails here and must either be converged on both planes in the
// same change, or pinned here with its rationale and added to the twin table
// in BASELINE_INVENTORY.md.
//
// What is compared and what is deliberately not:
//   - Columns (name, type, NOT NULL, PRIMARY KEY position): hard parity as a
//     set. Column ORDER is not compared (SQLite writers use named columns;
//     preparation_job_units appends its extension columns via ALTER on the
//     primary plane while the jobs copy inlines them).
//   - Column DEFAULT expressions: hard parity except the pinned pairs below
//     (the jobs copy drops non-constant defaults it could not express inline).
//   - CHECK constraints: hard parity except the pinned primary-only sets below
//     (the hand copy dropped them; SQLite cannot add CHECK via ALTER, so the
//     pinned state is a table-rebuild away and stays application-enforced).
//   - UNIQUE indexes: hard shape parity (unique + partial + columns), because
//     uniqueness is correctness — writer ON CONFLICT clauses bind to it.
//     Names are not compared (planes name their indexes independently).
//   - Non-unique indexes: NOT compared. Per-plane access patterns legitimately
//     differ (the jobs plane serves the execution hot path, the primary plane
//     serves resume/audit lookups). A non-unique index is a performance
//     choice, not a contract.
func TestMigrations_TwinSchemaParity(t *testing.T) {
	primaryDB := migratedTwinDB(t, "../../../migrations/sqlite", "primary")
	jobsDB := migratedTwinDB(t, "../../../migrations/sqlite_jobs", "jobs")

	for _, table := range twinTables {
		t.Run(table, func(t *testing.T) {
			primary := readTwinShape(t, primaryDB, table)
			jobs := readTwinShape(t, jobsDB, table)

			compareTwinColumns(t, table, primary, jobs)
			compareTwinChecks(t, table, primary, jobs)
			compareTwinUniqueIndexes(t, table, primary, jobs)
		})
	}

	// Third surface: the PostgreSQL media SSOT declares outbox_events a third
	// time. The engine types legitimately differ (BIGSERIAL vs INTEGER
	// AUTOINCREMENT), so only the two correctness-bearing properties are
	// pinned: the column-name set and the PARTIAL event_key unique index.
	t.Run("outbox_events_postgres_surface", func(t *testing.T) {
		pgColumns, pgUX := parsePostgresOutboxSurface(t)

		primary := readTwinShape(t, primaryDB, "outbox_events")
		primaryNames := make([]string, 0, len(primary.columns))
		for name := range primary.columns {
			primaryNames = append(primaryNames, name)
		}
		sort.Strings(primaryNames)
		pgNames := append([]string(nil), pgColumns...)
		sort.Strings(pgNames)
		require.Equal(t, primaryNames, pgNames,
			"migrations/postgres/001_media_schema.sql outbox_events must declare the same column-name set as the primary SQLite twin")

		require.True(t, pgUX.partial,
			"PG ux_outbox_events_event_key must stay PARTIAL (WHERE event_key <> '') — it is the cross-plane contract the Enqueue conflict target binds to (see 285_outbox_event_key_partial_unique.sql)")
	})
}

// TestMigrations_TwinOutboxEventKeyContract pins the event_key semantics both
// planes must share (repaired on the primary plane by
// 285_outbox_event_key_partial_unique.sql). The Enqueue writers on BOTH planes
// use `ON CONFLICT(event_key) WHERE event_key != ” DO NOTHING`:
//
//   - duplicate non-empty event_key → absorbed (idempotency arbiter);
//   - empty event_key marks one-shot inserts → NEVER deduplicated.
//
// Before 285 the primary full unique index silently absorbed the second
// empty-key insert while the jobs partial index kept it — identical shared
// code, different behaviour per plane. This test runs the writers' exact SQL
// shape against the live migrated schema of each plane.
func TestMigrations_TwinOutboxEventKeyContract(t *testing.T) {
	for _, plane := range []struct {
		name, dir, target string
	}{
		{"primary", "../../../migrations/sqlite", "primary"},
		{"jobs", "../../../migrations/sqlite_jobs", "jobs"},
	} {
		t.Run(plane.name, func(t *testing.T) {
			db := migratedTwinDB(t, plane.dir, plane.target)

			insert := func(eventKey, marker string) {
				t.Helper()
				_, err := db.Exec(
					`INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at)
					 VALUES (?, ?, ?, ?, ?, ?, ?)
					 ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING`,
					"test.event", marker, "test", "{}", eventKey, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
				require.NoError(t, err, "Enqueue-shape insert (event_key=%q)", eventKey)
			}
			countKeys := func(eventKey string) int {
				t.Helper()
				var n int
				require.NoError(t, db.QueryRow(
					`SELECT COUNT(*) FROM outbox_events WHERE event_key = ?`, eventKey).Scan(&n))
				return n
			}

			// One-shot inserts (empty event_key) are never deduplicated.
			insert("", "one-shot-1")
			insert("", "one-shot-2")
			require.Equal(t, 2, countKeys(""),
				"two one-shot inserts with empty event_key must both survive on every plane")

			// Non-empty event_key stays the idempotency arbiter.
			insert("arbiter-key", "first")
			insert("arbiter-key", "retry")
			require.Equal(t, 1, countKeys("arbiter-key"),
				"duplicate non-empty event_key must be absorbed on every plane")
		})
	}
}

// ── twin contract data ──────────────────────────────────────────────────────

// twinTables are the table families declared by hand on both SQLite planes.
var twinTables = []string{
	"artifact_stages",
	"job_checkpoints",
	"preparation_units",
	"preparation_job_units",
	"preparation_dependencies",
	"preparation_claim_snapshots",
	"preparation_attempts",
	"job_results",
	"outbox_events",
}

// twinPinnedDefaultDivergence pins DEFAULT expressions that legitimately
// differ between the two hand copies. Rationale: the jobs-plane rewrite
// (migrations/sqlite_jobs/001_jobs_plane.sql) states timestamps without
// defaults because the shared writers always set them explicitly; the primary
// copy keeps its historical defaults (147/119, mirrored by
// 000_baseline_267.sql). Keyed as table → column → [primary, jobs].
var twinPinnedDefaultDivergence = map[string]map[string][2]string{
	"artifact_stages": {
		"created_at": {"datetime('now')", ""},
		"updated_at": {"datetime('now')", ""},
	},
	"job_results": {
		"created_at": {"strftime('%Y-%m-%dT%H:%M:%fZ','now')", ""},
	},
}

// twinPinnedCheckDivergence pins CHECK constraints present only on the primary
// copy. Rationale: the jobs-plane rewrite dropped them (a hand copy written
// before the CHECKs existed cannot regain them via ALTER TABLE — SQLite has no
// ADD CONSTRAINT), and both planes enforce the same value sets at the
// application layer (typed state enums). Normalized as whitespace-collapsed
// CHECK (...) expressions in declaration order.
var twinPinnedCheckDivergence = map[string][]string{
	"artifact_stages": {
		"CHECK (requirement IN ('required','optional'))",
		"CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT'))",
	},
	"preparation_units": {
		"CHECK (state IN ('PLANNED', 'RUNNING', 'READY', 'FAILED', 'STALE'))",
		"CHECK (resource_class IN ( 'NETWORK', 'DISK', 'CPU_LIGHT', 'CPU_HEAVY', 'LLM', 'TTS', 'GPU', 'DRIVE' ))",
		"CHECK (speculation_level BETWEEN 0 AND 5)",
		"CHECK (cost_class IN ('CHEAP', 'MEDIUM', 'EXPENSIVE'))",
		"CHECK (result_kind IN ( 'NONE', 'ARTIFACT_CACHE', 'CONTENT_OBJECT', 'DOMAIN_CACHE', 'INLINE_JSON' ))",
	},
	"preparation_dependencies": {
		"CHECK (dependency_kind IN ('HARD', 'SOFT'))",
	},
	"preparation_attempts": {
		"CHECK (execution_mode IN ('SPECULATIVE', 'ACTIVE', 'ADOPTION_CHECK'))",
		"CHECK (status IN ('RUNNING', 'READY', 'FAILED', 'CANCELLED', 'PREEMPTED', 'HIT'))",
	},
}

// ── live schema reading ─────────────────────────────────────────────────────

type twinColumn struct {
	name    string
	typ     string
	notNull bool
	pk      int
	def     string // normalized PRAGMA table_info dflt_value
}

type twinIndexShape struct {
	name    string
	unique  bool
	partial bool
	cols    string // index_xinfo columns, direction-tagged, comma-joined
}

type twinShape struct {
	columns map[string]twinColumn
	checks  []string // normalized CHECK (...) expressions, declaration order
	indexes []twinIndexShape
}

// migratedTwinDB applies the plane's migration directory to a fresh database
// and returns a read-only handle to the live schema.
func migratedTwinDB(t *testing.T, migrationsDir, targetDB string) *sql.DB {
	t.Helper()
	absDir, err := filepath.Abs(migrationsDir)
	require.NoError(t, err, "resolve migrations dir %s", migrationsDir)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "twin.sqlite")
	log := zaptest.NewLogger(t)
	require.NoError(t, RunMigrationsOnDB(dbPath, log, filepath.Clean(absDir), targetDB),
		"RunMigrationsOnDB(%s, %s) must succeed on a fresh DB", migrationsDir, targetDB)
	db, err := sql.Open("sqlite3", dbPath+"?_mode=ro")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readTwinShape(t *testing.T, db *sql.DB, table string) twinShape {
	t.Helper()
	shape := twinShape{columns: map[string]twinColumn{}}

	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	require.NoError(t, err, "PRAGMA table_info(%s)", table)
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			defRaw           sql.NullString
		)
		require.NoError(t, rows.Scan(&cid, &name, &typ, &notNull, &defRaw, &pk))
		shape.columns[name] = twinColumn{
			name:    name,
			typ:     strings.ToUpper(strings.TrimSpace(typ)),
			notNull: notNull == 1,
			pk:      pk,
			def:     normalizeDefault(defRaw.String),
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.NotEmpty(t, shape.columns, "table %s must exist on the plane", table)

	// CHECK constraints: extracted from the live CREATE statement with a
	// balanced-paren scan, in declaration order.
	var createSQL string
	require.NoError(t, db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&createSQL),
		"sqlite_master must hold the CREATE statement for %s", table)
	shape.checks = extractCheckClauses(createSQL)

	idxRows, err := db.Query(`PRAGMA index_list(` + table + `)`)
	require.NoError(t, err, "PRAGMA index_list(%s)", table)
	type idxListRow struct {
		seq, unique, partial int
		name, origin         string
	}
	var listed []idxListRow
	for idxRows.Next() {
		var r idxListRow
		require.NoError(t, idxRows.Scan(&r.seq, &r.name, &r.unique, &r.origin, &r.partial))
		listed = append(listed, r)
	}
	require.NoError(t, idxRows.Err())
	require.NoError(t, idxRows.Close())

	for _, r := range listed {
		cols, err := db.Query(`PRAGMA index_xinfo(` + r.name + `)`)
		require.NoError(t, err, "PRAGMA index_xinfo(%s)", r.name)
		var colParts []string
		for cols.Next() {
			var seq, cid, desc, key int
			var name, coll sql.NullString
			require.NoError(t, cols.Scan(&seq, &cid, &name, &desc, &coll, &key))
			if key == 0 {
				continue // auxiliary column (e.g. rowid payload)
			}
			dir := ""
			if desc == 1 {
				dir = " DESC"
			}
			colParts = append(colParts, name.String+dir)
		}
		require.NoError(t, cols.Err())
		require.NoError(t, cols.Close())
		shape.indexes = append(shape.indexes, twinIndexShape{
			name:    r.name,
			unique:  r.unique == 1,
			partial: r.partial == 1,
			cols:    strings.Join(colParts, ","),
		})
	}
	return shape
}

// checkClauseRe anchors on the CHECK keyword with a word boundary so table
// and column names containing "check" (job_checkpoints, checkpoint_stage…)
// are never mistaken for constraints.
var checkClauseRe = regexp.MustCompile(`(?i)\bCHECK\s*\(`)

// extractCheckClauses returns every CHECK (...) expression of a CREATE TABLE
// statement, whitespace-collapsed, in declaration order.
func extractCheckClauses(createSQL string) []string {
	var out []string
	searchFrom := 0
	for {
		loc := checkClauseRe.FindStringIndex(createSQL[searchFrom:])
		if loc == nil {
			break
		}
		open := searchFrom + strings.IndexByte(createSQL[searchFrom+loc[0]:], '(') + loc[0]
		depth := 0
		end := -1
		for k := open; k < len(createSQL); k++ {
			switch createSQL[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = k
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			break
		}
		expr := "CHECK " + strings.TrimSpace(createSQL[open:end+1])
		out = append(out, normalizeWhitespace(expr))
		searchFrom = end + 1
	}
	return out
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// normalizeDefault collapses the PRAGMA dflt_value spelling so the same
// expression compares equal regardless of formatting: whitespace is
// collapsed and redundant outer parentheses are stripped.
func normalizeDefault(s string) string {
	s = normalizeWhitespace(s)
	for {
		if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
			return s
		}
		depth := 0
		balanced := true
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i < len(s)-1 {
					balanced = false
				}
			}
		}
		if !balanced || depth != 0 {
			return s
		}
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
}

// ── comparisons ─────────────────────────────────────────────────────────────

func compareTwinColumns(t *testing.T, table string, primary, jobs twinShape) {
	t.Helper()
	primaryNames := columnNames(primary)
	jobsNames := columnNames(jobs)
	require.Equal(t, primaryNames, jobsNames,
		"twin columns of %s diverged between planes — converge BOTH declarations (canonical: migrations/sqlite, projection: migrations/sqlite_jobs/001_jobs_plane.sql) or pin the divergence in this test + BASELINE_INVENTORY.md", table)

	pinned := twinPinnedDefaultDivergence[table]
	for _, name := range primaryNames {
		pc, jc := primary.columns[name], jobs.columns[name]
		require.Equal(t, pc.typ, jc.typ,
			"twin column %s.%s: type diverged (%s vs %s)", table, name, pc.typ, jc.typ)
		require.Equal(t, pc.notNull, jc.notNull,
			"twin column %s.%s: NOT NULL diverged", table, name)
		require.Equal(t, pc.pk, jc.pk,
			"twin column %s.%s: PRIMARY KEY position diverged", table, name)

		if pc.def == jc.def {
			continue
		}
		want, ok := pinned[name]
		require.True(t, ok,
			"twin column %s.%s: DEFAULT diverged (%q primary vs %q jobs) and is not pinned — converge both declarations or pin it with rationale in twinPinnedDefaultDivergence + BASELINE_INVENTORY.md",
			table, name, pc.def, jc.def)
		require.Equal(t, want[0], pc.def,
			"pinned DEFAULT for %s.%s no longer matches the primary plane", table, name)
		require.Equal(t, want[1], jc.def,
			"pinned DEFAULT for %s.%s no longer matches the jobs plane", table, name)
	}
	for name := range pinned {
		_, ok := primary.columns[name]
		require.True(t, ok, "pinned DEFAULT divergence names unknown column %s.%s", table, name)
		if primary.columns[name].def == jobs.columns[name].def {
			require.Fail(t, "stale pin",
				"twin column %s.%s: the pinned DEFAULT divergence has been converged — drop it from twinPinnedDefaultDivergence + BASELINE_INVENTORY.md", table, name)
		}
	}
}

func compareTwinChecks(t *testing.T, table string, primary, jobs twinShape) {
	t.Helper()
	pinned := twinPinnedCheckDivergence[table]
	require.Equal(t, pinned, primary.checks,
		"primary CHECK constraints of %s no longer match the pinned set — update twinPinnedCheckDivergence + BASELINE_INVENTORY.md to the new primary contract (and mirror it on the jobs plane if the CHECKs were converged)", table)
	require.Empty(t, jobs.checks,
		"the jobs copy of %s must carry no CHECK constraints (the pinned divergence) — if it gained some, either they match the primary set (then converge the pin to full parity and delete the divergence entry) or they are new drift", table)
}

func compareTwinUniqueIndexes(t *testing.T, table string, primary, jobs twinShape) {
	t.Helper()
	shapeOf := func(s twinShape) []string {
		var out []string
		for _, idx := range s.indexes {
			if !idx.unique {
				continue
			}
			partial := "full"
			if idx.partial {
				partial = "partial"
			}
			out = append(out, fmt.Sprintf("%s on (%s)", partial, idx.cols))
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, shapeOf(primary), shapeOf(jobs),
		"UNIQUE index shapes of %s diverged between planes — uniqueness is correctness (writer ON CONFLICT clauses bind to it): converge both declarations or document the divergence in this test + BASELINE_INVENTORY.md", table)
}

func columnNames(s twinShape) []string {
	names := make([]string, 0, len(s.columns))
	for name := range s.columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ── PostgreSQL third surface ────────────────────────────────────────────────

var (
	pgOutboxCreateRe = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS outbox_events \((.*?)\n\);`)
	pgOutboxUXRe     = regexp.MustCompile(`(?s)CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key\s+ON outbox_events \([^)]*\)\s+WHERE ([^;]*);`)
)

// parsePostgresOutboxSurface extracts the outbox_events column-name set and
// the ux_outbox_events_event_key shape from the PostgreSQL media SSOT.
func parsePostgresOutboxSurface(t *testing.T) (columns []string, ux twinIndexShape) {
	t.Helper()
	raw, err := os.ReadFile("../../../migrations/postgres/001_media_schema.sql")
	require.NoError(t, err, "read PG media schema")

	m := pgOutboxCreateRe.FindStringSubmatch(string(raw))
	require.NotNil(t, m, "001_media_schema.sql must declare CREATE TABLE IF NOT EXISTS outbox_events")
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		name := strings.Fields(line)[0]
		name = strings.Trim(name, ",")
		if name == "" {
			continue
		}
		columns = append(columns, name)
	}
	require.NotEmpty(t, columns)

	ux = twinIndexShape{name: "ux_outbox_events_event_key", unique: true}
	if um := pgOutboxUXRe.FindStringSubmatch(string(raw)); um != nil {
		ux.partial = strings.TrimSpace(um[1]) != ""
		ux.cols = "event_key"
	}
	return columns, ux
}
