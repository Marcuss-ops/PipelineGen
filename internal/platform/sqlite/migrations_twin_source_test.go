package sqlite

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/twinddl"
	"github.com/stretchr/testify/require"
)

// TestMigrations_TwinSourceMatchesLiveSchema is the live leg of the shared
// twin DDL source (internal/platform/sqlite/twinddl): the schema a REAL
// migrated database ends up with must equal the shape the shared source
// declares — canonical on the primary plane, pinned projection on the jobs
// plane. TestMigrations_TwinSchemaParity proves the two planes agree with each
// other modulo the pinned divergences; this test anchors both to the single
// embedded source, so the hand copies can never drift from it unnoticed.
func TestMigrations_TwinSourceMatchesLiveSchema(t *testing.T) {
	canonical, err := twinddl.CanonicalShapes()
	require.NoError(t, err, "twinddl canonical source must parse")
	projection, err := twinddl.JobsPlaneShapes()
	require.NoError(t, err, "twinddl jobs projection must derive")

	primaryDB := migratedTwinDB(t, "../../../migrations/sqlite", "primary")
	jobsDB := migratedTwinDB(t, "../../../migrations/sqlite_jobs", "jobs")

	for _, table := range twinddl.TwinTables {
		t.Run(table, func(t *testing.T) {
			compareLiveToTwinSource(t, primaryDB, "primary", table, readTwinShape(t, primaryDB, table), canonical[table])
			compareLiveToTwinSource(t, jobsDB, "jobs", table, readTwinShape(t, jobsDB, table), projection[table])
		})
	}
}

// liveIndexWhereRe finds the WHERE predicate of a CREATE UNIQUE INDEX
// statement (after the column list).
var liveIndexWhereRe = regexp.MustCompile(`(?is)\)\s*WHERE\s+(.*?);?\s*$`)

// compareLiveToTwinSource compares a live PRAGMA-read shape with a twinddl
// source shape under the twin contract (columns as a set, CHECKs in
// declaration order, UNIQUE index shapes incl. the partial predicate).
func compareLiveToTwinSource(t *testing.T, db *sql.DB, plane, table string, live twinShape, want twinddl.TableShape) {
	t.Helper()
	got := twinddl.TableShape{Name: table}
	for name, col := range live.columns {
		c := twinddl.Column{
			Name:    strings.ToLower(name),
			Type:    strings.ToUpper(strings.TrimSpace(col.typ)),
			NotNull: col.notNull,
			PK:      col.pk,
		}
		if def := strings.TrimSpace(col.def); def != "" {
			c.HasDefault = true
			c.Default = twinddl.NormalizeDefault(def)
		}
		got.Columns = append(got.Columns, c)
	}
	for _, chk := range live.checks {
		got.Checks = append(got.Checks, twinddl.NormalizeCheck(chk))
	}
	for _, idx := range live.indexes {
		if !idx.unique {
			continue
		}
		if strings.HasPrefix(idx.name, "sqlite_autoindex_") {
			continue // PK shadow index: already compared as column PK positions
		}
		got.Uniques = append(got.Uniques, twinddl.UniqueIndex{
			Name:    idx.name,
			Cols:    idx.cols,
			Partial: idx.partial,
			Where:   liveIndexWhere(t, db, idx.name),
		})
	}

	if diff := twinddl.ShapesEqual(got, want); diff != "" {
		t.Errorf("live %s schema of %s drifted from the shared twin DDL source (%s)\n"+
			"converge the migration declarations AND twin_tables.sql in one change (then re-run cmd/twin-ddl-gen --generate)",
			plane, table, diff)
	}
}

// liveIndexWhere extracts the normalised WHERE predicate of a live unique
// index from its sqlite_master statement ("" for a full unique index).
func liveIndexWhere(t *testing.T, db *sql.DB, index string) string {
	t.Helper()
	var ddl sql.NullString
	require.NoError(t, db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&ddl),
		"sqlite_master must hold the CREATE statement for index %s", index)
	if !ddl.Valid {
		return ""
	}
	if m := liveIndexWhereRe.FindStringSubmatch(ddl.String); m != nil {
		return twinddl.NormalizeExpr(m[1])
	}
	return ""
}
