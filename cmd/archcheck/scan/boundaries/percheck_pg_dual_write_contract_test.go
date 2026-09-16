// Package scan — companion test for percheck_pg_dual_write_contract.go.
//
// Pins:
//
//	(a) the pair list is a faithful transcription of migration 004, so the gate
//	    cannot decay into a hand-maintained list that no longer matches the
//	    schema it protects;
//	(b) "half write trips" — the dangerous direction (TEXT written, mirror not)
//	    in an UPDATE SET clause;
//	(c) "bind mismatch trips" — both halves written from DIFFERENT placeholders;
//	(d) "same bind passes" — the canonical TEXT = $N / mirror = NULLIF($N,'') form;
//	(e) "sibling + now() pass" — the ON CONFLICT excluded.<col>_ts form and the
//	    transaction-timestamp form used by the Drive delivery updates;
//	(f) "self-derived mirror passes" — a mirror-only refresh that reads the TEXT
//	    column cannot diverge;
//	(g) "mirror-only from a bind trips" — the other divergence direction;
//	(h) "INSERT column list trips" — listing a TEXT timestamp without its mirror;
//	(i) "SQLite dialect is exempt" — ?-bound statements have no *_ts columns;
//	(j) "non-004 table is exempt" — the gate is scoped to the three tables 004
//	    touches;
//	(k) comments and *_test.go are out of scope;
//	(l) the REAL repository tree is clean under this gate.
package boundaries

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanPGDualWrite(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanPGDualWriteContract(root, &policy.Policy{}, r)
	return r
}

func pgDualWriteViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == pgDualWriteRule {
			out = append(out, v)
		}
	}
	return out
}

func writePGDualWriteFixture(t *testing.T, root, body string) {
	t.Helper()
	writeGoFile(t, root, "internal/capabilities/nouveau/dualwrite.go", body)
}

func pgDualWriteFixture(body string) string {
	return "package nouveau\n\nconst q = `" + body + "`\n"
}

// TestPGDualWritePairsMatchMigration004 is the drift pin: every TIMESTAMPTZ
// expand column that migrations/postgres/004_media_timestamps_timestamptz.sql
// declares must appear in pgDualWritePairs, paired with the TEXT column its name
// derives from. Adding a mirror column to the migration without extending the
// gate — or vice versa — fails here.
func TestPGDualWritePairsMatchMigration004(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "migrations", "postgres", "004_media_timestamps_timestamptz.sql"))
	if err != nil {
		t.Skipf("migration not resolvable from the test cwd: %v", err)
	}
	decl := regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)\s+TIMESTAMPTZ`)
	declared := map[string]map[string]bool{}
	for _, m := range decl.FindAllStringSubmatch(string(raw), -1) {
		table, mirror := strings.ToLower(m[1]), strings.ToLower(m[2])
		if declared[table] == nil {
			declared[table] = map[string]bool{}
		}
		declared[table][mirror] = true
	}
	if len(declared) == 0 {
		t.Fatal("no TIMESTAMPTZ expand columns parsed from migration 004; the pin would be vacuous")
	}
	for table, mirrors := range declared {
		got := map[string]bool{}
		for _, pair := range pgDualWritePairs[table] {
			got[pair.mirror] = true
			if want := pair.text + "_ts"; pair.mirror != want {
				t.Errorf("%s: mirror %q does not derive from TEXT column %q (want %q)", table, pair.mirror, pair.text, want)
			}
		}
		for mirror := range mirrors {
			if !got[mirror] {
				t.Errorf("%s: migration 004 declares %q but pgDualWritePairs does not", table, mirror)
			}
		}
		for mirror := range got {
			if !mirrors[mirror] {
				t.Errorf("%s: pgDualWritePairs lists %q but migration 004 does not declare it", table, mirror)
			}
		}
	}
	// And no table may be invented: the gate only protects what 004 touches.
	for table := range pgDualWritePairs {
		if declared[table] == nil {
			t.Errorf("pgDualWritePairs covers %q, which migration 004 does not declare", table)
		}
	}
}

// TestScanPGDualWriteContract_HalfWriteTrips pins the dangerous direction: the
// TEXT timestamp advances and the mirror goes stale.
func TestScanPGDualWriteContract_HalfWriteTrips(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"UPDATE media_assets SET lifecycle_state = $1, updated_at = $2 WHERE id = $3"))

	got := pgDualWriteViolations(scanPGDualWrite(t, tmp))
	if len(got) != 1 {
		t.Fatalf("violations = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].MatchedRule != "dual_write_half_write" {
		t.Errorf("MatchedRule = %q, want dual_write_half_write", got[0].MatchedRule)
	}
	if got[0].Line == 0 {
		t.Errorf("expected a non-zero line number, got %+v", got[0])
	}
}

// TestScanPGDualWriteContract_BindMismatchTrips pins that writing both halves
// from different parameters is caught, not merely a missing mirror.
func TestScanPGDualWriteContract_BindMismatchTrips(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"UPDATE media_assets SET updated_at = $1, updated_at_ts = NULLIF($2, '')::timestamptz WHERE id = $3"))

	got := pgDualWriteViolations(scanPGDualWrite(t, tmp))
	if len(got) != 1 {
		t.Fatalf("violations = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].MatchedRule != "dual_write_bind_mismatch" {
		t.Errorf("MatchedRule = %q, want dual_write_bind_mismatch", got[0].MatchedRule)
	}
}

// TestScanPGDualWriteContract_SameSourcePasses pins the accepted shapes: the
// canonical same-bind dual-write, the ON CONFLICT excluded.<col>_ts form, the
// transaction-timestamp form and a self-derived mirror-only refresh.
func TestScanPGDualWriteContract_SameSourcePasses(t *testing.T) {
	cases := map[string]string{
		"same_bind": "UPDATE media_assets SET updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz WHERE id = $1",
		"insert_both": "INSERT INTO asset_locations (asset_id, created_at, updated_at, created_at_ts, updated_at_ts) " +
			"VALUES ($1, $2, $3, NULLIF($2, '')::timestamptz, NULLIF($3, '')::timestamptz) " +
			"ON CONFLICT (asset_id, location_kind) DO UPDATE SET updated_at = excluded.updated_at, updated_at_ts = excluded.updated_at_ts",
		"transaction_now":     "UPDATE media_assets SET updated_at = to_char(now() AT TIME ZONE 'UTC', 'YYYY'), updated_at_ts = now() WHERE id = $1",
		"self_derived_mirror": "UPDATE media_assets SET group_name = $1, updated_at_ts = NULLIF(updated_at, '')::timestamptz WHERE id = $2",
		"lease_reset":         "UPDATE outbox_events SET lease_expiry = NULL, lease_expiry_ts = NULL, updated_at = $1, updated_at_ts = NULLIF($1, '')::timestamptz WHERE id = $2",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			writePGDualWriteFixture(t, tmp, pgDualWriteFixture(body))
			if got := pgDualWriteViolations(scanPGDualWrite(t, tmp)); len(got) != 0 {
				t.Fatalf("violations = %d, want 0: %+v", len(got), got)
			}
		})
	}
}

// TestScanPGDualWriteContract_MirrorOnlyFromBindTrips pins the other divergence
// direction: the typed column is moved without advancing the TEXT column.
func TestScanPGDualWriteContract_MirrorOnlyFromBindTrips(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"UPDATE outbox_events SET completed_at_ts = NULLIF($1, '')::timestamptz WHERE id = $2"))

	got := pgDualWriteViolations(scanPGDualWrite(t, tmp))
	if len(got) != 1 {
		t.Fatalf("violations = %d, want exactly 1: %+v", len(got), got)
	}
	if got[0].MatchedRule != "dual_write_mirror_without_text" {
		t.Errorf("MatchedRule = %q, want dual_write_mirror_without_text", got[0].MatchedRule)
	}
}

// TestScanPGDualWriteContract_InsertColumnListTrips pins that an INSERT which
// lists a TEXT timestamp without its mirror is caught per pair.
func TestScanPGDualWriteContract_InsertColumnListTrips(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"INSERT INTO asset_locations (asset_id, created_at, updated_at) VALUES ($1, $2, $3)"))

	got := pgDualWriteViolations(scanPGDualWrite(t, tmp))
	if len(got) != 2 {
		t.Fatalf("violations = %d, want 2 (created_at + updated_at): %+v", len(got), got)
	}
	for _, v := range got {
		if v.MatchedRule != "insert_missing_mirror" {
			t.Errorf("MatchedRule = %q, want insert_missing_mirror", v.MatchedRule)
		}
	}
}

// TestScanPGDualWriteContract_SQLiteDialectExempt pins the dialect scoping: the
// operational schema has no *_ts columns, so a ?-bound statement is not debt.
func TestScanPGDualWriteContract_SQLiteDialectExempt(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"UPDATE media_assets SET lifecycle_state = ?, updated_at = ? WHERE id = ?"))

	if got := pgDualWriteViolations(scanPGDualWrite(t, tmp)); len(got) != 0 {
		t.Fatalf("SQLite statement reported as debt: %+v", got)
	}
}

// TestScanPGDualWriteContract_Non004TableExempt pins the scoping to the tables
// migration 004 touches.
func TestScanPGDualWriteContract_Non004TableExempt(t *testing.T) {
	tmp := t.TempDir()
	writePGDualWriteFixture(t, tmp, pgDualWriteFixture(
		"UPDATE asset_renditions SET updated_at = $1 WHERE asset_id = $2"))

	if got := pgDualWriteViolations(scanPGDualWrite(t, tmp)); len(got) != 0 {
		t.Fatalf("non-004 table reported as debt: %+v", got)
	}
}

// TestScanPGDualWriteContract_CommentAndTestFileExempt pins that prose naming the
// statement is not a write, and that *_test.go is out of scope.
func TestScanPGDualWriteContract_CommentAndTestFileExempt(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/capabilities/nouveau/doc.go", `package nouveau

// Example: UPDATE media_assets SET updated_at = '...' WHERE id = '...'
func doc() {}
`)
	writeGoFile(t, tmp, "internal/capabilities/nouveau/dualwrite_test.go", `package nouveau

const q = "UPDATE media_assets SET updated_at = $1 WHERE id = $2"
`)

	if got := pgDualWriteViolations(scanPGDualWrite(t, tmp)); len(got) != 0 {
		t.Fatalf("comment/test-only statement reported as debt: %+v", got)
	}
}

// TestScanPGDualWriteContract_RepoTreeIsClean is the integration pin: the real
// repository must satisfy the gate. A failure means a new mutation wrote one half
// of a migration-004 pair without the other.
func TestScanPGDualWriteContract_RepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable from the test cwd: %v", err)
	}
	got := pgDualWriteViolations(scanPGDualWrite(t, root))
	if len(got) == 0 {
		return
	}
	var b strings.Builder
	for _, v := range got {
		b.WriteString("\n  " + v.File + ":" + strconv.Itoa(v.Line) + " [" + v.MatchedRule + "]")
	}
	t.Fatalf("%d migration-004 dual-write contract violation(s):%s", len(got), b.String())
}
