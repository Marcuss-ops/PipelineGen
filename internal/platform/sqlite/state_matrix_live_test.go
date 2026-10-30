package sqlite

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/statematrix"
	"go.uber.org/zap"
)

// TestStateMatrixMatchesLiveSchema is the live-schema enforcement of
// docs/migrations/STATE_MATRIX.md (item: "matrice stati — valida lo schema
// vivo"). It migrates a FRESH primary DB and a FRESH jobs DB through the
// production migration chain (migrateAll) and asserts, for every matrix row
// on those planes:
//
//  1. coverage — every live non-legacy state column appears in the matrix,
//     and every matrix row exists in the live schema;
//  2. default  — the live column DEFAULT equals the matrix default;
//  3. values   — the live CHECK (col IN (...)) vocabulary equals the matrix
//     values exactly (a declared vocabulary is a contract);
//  4. casing   — the live values/default share the matrix casing family.
//
// PostgreSQL rows are out of scope here (no live PG in unit tests); their
// static leg is pinned by the archcheck gate percheck_state_matrix.
func TestStateMatrixMatchesLiveSchema(t *testing.T) {
	rows := loadStateMatrix(t)

	live := map[string]map[string]statematrix.ColumnDef{} // plane -> table.column -> def
	for _, target := range []struct {
		plane string
		db    string
		dir   string
	}{
		{"sqlite", "primary", testMigrationsDir()},
		{"sqlite_jobs", "jobs", resolveJobsMigrationsDir()},
	} {
		db := NewTestDB(t, nil)
		if err := migrateAll(db, zap.NewNop(), target.dir, target.db); err != nil {
			t.Fatalf("%s: migrate: %v", target.plane, err)
		}
		defer func() { _ = db.Close() }()
		tables, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			t.Fatal(err)
		}
		planeCols := map[string]statematrix.ColumnDef{}
		for tables.Next() {
			var name string
			var sqlPtr *string
			if err := tables.Scan(&name, &sqlPtr); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(name, "legacy_") {
				continue // quarantine material, out of matrix scope
			}
			sql := ""
			if sqlPtr != nil {
				sql = *sqlPtr
			}
			for _, def := range statematrix.ExtractStateColumns(sql) {
				planeCols[def.Table+"."+def.Name] = def
			}
		}
		_ = tables.Close()
		live[target.plane] = planeCols
	}

	matrixIndex := map[string]statematrix.Row{} // table.column@plane -> row
	for _, r := range rows {
		for _, plane := range r.Planes {
			if plane == "postgres" {
				continue
			}
			matrixIndex[r.Table+"."+r.Column+"@"+plane] = r
		}
	}

	for plane, cols := range live {
		for key, def := range cols {
			row, ok := matrixIndex[key+"@"+plane]
			if !ok {
				t.Errorf("coverage: live %s column %s is missing from docs/migrations/STATE_MATRIX.md", plane, key)
				continue
			}
			checkStateMatrixRow(t, plane, key, row, def)
		}
		for key := range matrixIndex {
			if !strings.HasSuffix(key, "@"+plane) {
				continue
			}
			col := strings.TrimSuffix(key, "@"+plane)
			if _, ok := cols[col]; !ok {
				t.Errorf("coverage: matrix row %s (plane %s) has no such live column", col, plane)
			}
		}
	}
}

// checkStateMatrixRow asserts default/casing/values agreement between one
// matrix row and its live column declaration.
func checkStateMatrixRow(t *testing.T, plane, key string, row statematrix.Row, def statematrix.ColumnDef) {
	t.Helper()
	if row.HasDefault != def.HasDefault || (row.HasDefault && row.Default != def.Default) {
		t.Errorf("%s %s: default drift: matrix=%q(has=%v) live=%q(has=%v)",
			plane, key, row.Default, row.HasDefault, def.Default, def.HasDefault)
	}
	if def.HasCheck {
		if !sameStringSet(row.ValueList, def.ValueList) {
			t.Errorf("%s %s: values drift: matrix=%v live(CHECK)=%v", plane, key, row.ValueList, def.ValueList)
		}
	} else if len(row.ValueList) > 0 {
		t.Errorf("%s %s: matrix declares values %v but the live schema has no CHECK (col IN ...)", plane, key, row.ValueList)
	}
	samples := append([]string{}, def.ValueList...)
	if def.HasDefault {
		samples = append(samples, def.Default)
	}
	if want := statematrix.CasingFamily(samples...); row.Casing != "-" && want != row.Casing {
		t.Errorf("%s %s: casing drift: matrix=%s live=%s (samples %v)", plane, key, row.Casing, want, samples)
	}
}

// sameStringSet compares two string slices as sets.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string{}, a...)
	bs := append([]string{}, b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// loadStateMatrix reads and parses docs/migrations/STATE_MATRIX.md, failing
// the test when the machine section is unreadable (fail-closed: a matrix
// nobody can parse is a matrix nobody enforces).
func loadStateMatrix(t *testing.T) []statematrix.Row {
	t.Helper()
	path := stateMatrixDocPath(t)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	rows, err := statematrix.Parse(f)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: machine section is empty", path)
	}
	return rows
}

// stateMatrixDocPath resolves docs/migrations/STATE_MATRIX.md by walking up
// from this source file (mirrors testMigrationsDir).
func stateMatrixDocPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("state matrix: cannot resolve caller path")
	}
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "docs", "migrations", "STATE_MATRIX.md")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("state matrix: docs/migrations/STATE_MATRIX.md not found above %s", file)
		}
		dir = parent
	}
}
