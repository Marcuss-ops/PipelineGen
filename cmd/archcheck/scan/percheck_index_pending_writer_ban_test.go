// Package scan — percheck_index_pending_writer_ban_test.go pins the
// forward-prevention contract for the StateIndexPending writer ban.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func indexPendingWriterTestReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

func indexPendingWriterWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for relPath, contents := range files {
		fullPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", fullPath, err)
		}
	}
}

func indexPendingWriterViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == "percheck_index_pending_writer_ban" {
			out = append(out, v)
		}
	}
	return out
}

// TestIndexPendingWriter_ProductionWriterFails verifies that a
// production writer assigning asset.StateIndexPending trips the gate.
func TestIndexPendingWriter_ProductionWriterFails(t *testing.T) {
	dir := t.TempDir()
	indexPendingWriterWriteTree(t, dir, map[string]string{
		"internal/application/images/ingest.go": `package images
import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
func initAsset() { a := asset.ImageAsset{IndexState: asset.StateIndexPending} }
`,
	})
	r := indexPendingWriterTestReport()
	ScanIndexPendingWriterBan(dir, &policy.Policy{}, r)
	viol := indexPendingWriterViolations(r)
	if len(viol) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(viol), r.Violations)
	}
	if viol[0].File != "internal/application/images/ingest.go" {
		t.Fatalf("want violation in internal/application/images/ingest.go, got %q", viol[0].File)
	}
}

// TestIndexPendingWriter_CanonicalOwnerPasses verifies that the
// canonical domain definition is exempt.
func TestIndexPendingWriter_CanonicalOwnerPasses(t *testing.T) {
	dir := t.TempDir()
	indexPendingWriterWriteTree(t, dir, map[string]string{
		"internal/domain/asset/index_state.go": `package asset
const StateIndexPending IndexState = "INDEX_PENDING"
`,
	})
	r := indexPendingWriterTestReport()
	ScanIndexPendingWriterBan(dir, &policy.Policy{}, r)
	if got := len(indexPendingWriterViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside canonical owner, got %d: %+v", got, r.Violations)
	}
}

// TestIndexPendingWriter_HealthResolverPasses verifies that the
// health resolver that reads the legacy state is exempt.
func TestIndexPendingWriter_HealthResolverPasses(t *testing.T) {
	dir := t.TempDir()
	indexPendingWriterWriteTree(t, dir, map[string]string{
		"internal/application/assets/operator/index_health_resolver.go": `package operator
import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
func health(_ asset.IndexState) bool { return _ == asset.StateIndexPending }
`,
	})
	r := indexPendingWriterTestReport()
	ScanIndexPendingWriterBan(dir, &policy.Policy{}, r)
	if got := len(indexPendingWriterViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside health resolver, got %d: %+v", got, r.Violations)
	}
}

// TestIndexPendingWriter_TestFilesExempted verifies that production
// gate logic does not apply to test files.
func TestIndexPendingWriter_TestFilesExempted(t *testing.T) {
	dir := t.TempDir()
	indexPendingWriterWriteTree(t, dir, map[string]string{
		"internal/application/images/ingest_test.go": `package images
import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
func TestX() { s := asset.StateIndexPending }
`,
	})
	r := indexPendingWriterTestReport()
	ScanIndexPendingWriterBan(dir, &policy.Policy{}, r)
	if got := len(indexPendingWriterViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside test files, got %d: %+v", got, r.Violations)
	}
}
