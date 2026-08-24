package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeControlPlaneScanFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanControlPlaneSQLWritesRejectsNonCanonicalMultilineMutation(t *testing.T) {
	root := t.TempDir()
	body := "package example\n\nfunc mutate(db interface{ ExecContext(any, string, ...any) (any, error) }) {\n\tquery := `UPDATE\n\t\tmedia_assets SET name = ? WHERE id = ?`\n\t_, _ = db.ExecContext(nil, query)\n}\n"
	writeControlPlaneScanFixture(t, root, "internal/application/example/service.go", body)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 1 {
		t.Fatalf("expected one direct-write violation, got %d: %+v", len(report.Violations), report.Violations)
	}
	violation := report.Violations[0]
	if violation.Rule != controlPlaneSQLWritesRule || violation.MatchedRule != "non_canonical_update:media_assets" {
		t.Fatalf("unexpected violation: %+v", violation)
	}
	if violation.File != "internal/application/example/service.go" {
		t.Fatalf("unexpected file: %q", violation.File)
	}
}

func TestScanControlPlaneSQLWritesAllowsCanonicalRepository(t *testing.T) {
	root := t.TempDir()
	writeControlPlaneScanFixture(t, root, "internal/platform/sqlite/example/repository.go", `package example

func mutate() {
	_ = "DELETE FROM media_assets WHERE id = ?"
}
`)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 0 {
		t.Fatalf("canonical repository must be exempt: %+v", report.Violations)
	}
}

func TestScanControlPlaneSQLWritesDoesNotAllowNearMatchCanonicalFile(t *testing.T) {
	root := t.TempDir()
	writeControlPlaneScanFixture(t, root, "internal/application/jobs/finalizer/job_completion_writer.go.bak.go", `package finalizer

func mutate(db interface{ ExecContext(any, string, ...any) (any, error) }) {
	query := "UPDATE media_assets SET name = ?"
	_, _ = db.ExecContext(nil, query)
}
`)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 1 {
		t.Fatalf("near-match canonical paths must not be exempt: %+v", report.Violations)
	}
}

func TestScanControlPlaneSQLWritesIgnoresCommentsAndTests(t *testing.T) {
	root := t.TempDir()
	writeControlPlaneScanFixture(t, root, "internal/application/example/comments.go", `package example

// UPDATE media_assets SET name = 'documentation'
/* INSERT INTO jobs (id) VALUES ('documentation') */
func clean() {}
`)
	writeControlPlaneScanFixture(t, root, "internal/application/example/fixture_test.go", `package example

func seed() { _ = "INSERT INTO jobs (id) VALUES ('fixture')" }
`)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 0 {
		t.Fatalf("comments and tests must not be treated as production writes: %+v", report.Violations)
	}
}

func TestScanControlPlaneSQLWritesKeepsSQLCommentsInsideRawStrings(t *testing.T) {
	root := t.TempDir()
	writeControlPlaneScanFixture(t, root, "internal/application/example/raw_sql.go", `package example

func mutate(db interface{ ExecContext(any, string, ...any) (any, error) }) {
	query := `+"`"+`/* SQL optimizer hint */
UPDATE media_assets SET name = ? WHERE id = ?`+"`"+`
	_, _ = db.ExecContext(nil, query)
}
`)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 1 {
		t.Fatalf("SQL inside raw strings must remain visible to the scanner: %+v", report.Violations)
	}
}

func TestScanControlPlaneSQLWritesFailsClosedOnUnparseableProductionGo(t *testing.T) {
	root := t.TempDir()
	writeControlPlaneScanFixture(t, root, "internal/application/example/broken.go", `package example

func broken( {
	query := "UPDATE media_assets SET name = ?"
	_, _ = db.ExecContext(nil, query)
}
`)

	report := &report.Report{}
	ScanControlPlaneSQLWrites(root, &policy.Policy{}, report, true)
	if len(report.Violations) != 1 {
		t.Fatalf("unparseable production Go must fail closed: %+v", report.Violations)
	}
	if report.Violations[0].MatchedRule != "unparseable_production_go" {
		t.Fatalf("unexpected parse failure violation: %+v", report.Violations[0])
	}
}
