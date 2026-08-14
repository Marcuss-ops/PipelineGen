// Package scan — test for ScanUpsertPointsSoleOwner
// (PR-DIAGNOSI-FINALE rule 4).
//
// Hermetic (t.TempDir-anchored). Validates the four core
// invariants of the UpsertPoints sole-owner gate:
//
//  1. The canonical IndexingHandler caller surface is EXEMPT
//     (no violations).
//  2. A non-canonical producer that calls `client.UpsertPoints(`
//     trips the gate as SeverityError.
//  3. The transport-package function definition line
//     (`func (c *Client) UpsertPoints(...)`) is naturally
//     exempt (the regex requires a dot-receiver before the
//     call site name).
//  4. Test fixtures are exempt (residue documented in
//     migrations/api/archcheck-strict-baseline.json).
//  5. Comment-only references are residue-accounted (WARN in
//     !productionOnly mode; silenced in productionOnly mode).
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeFileForUpsertPointsTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanUpsertPointsSoleOwner_CanonicalExempt verifies
// the canonical IndexingHandler caller surface emitting
// `.UpsertPoints(` is EXEMPT.
func TestScanUpsertPointsSoleOwner_CanonicalExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/infrastructure/qdrant/indexing/index_writer_ops.go",
		`package indexing
import "fmt"
type Writer struct{}
func (w *Writer) Write() {
	fmt.Println(w.client.UpsertPoints)
}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("canonical caller surface tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanUpsertPointsSoleOwner_NonCanonicalCaller verifies
// a non-canonical producer emitting `client.UpsertPoints(`
// trips the gate.
func TestScanUpsertPointsSoleOwner_NonCanonicalCaller(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/application/random_other/bad_caller.go",
		`package random_other
import "fmt"
type FakeClient struct{}
func (c *FakeClient) Work() {
	fmt.Println(c.UpsertPoints("hi"))
}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("non-canonical caller did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != upsertPointsSoleOwnerRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, upsertPointsSoleOwnerRule)
	}
}

// TestScanUpsertPointsSoleOwner_TransportDefinitionExempt
// verifies the transport-package function definition line
// is naturally exempt (no dot-receiver → no trip).
func TestScanUpsertPointsSoleOwner_TransportDefinitionExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/infrastructure/qdrant/transport/client_points.go",
		`package transport
import "context"
type Client struct{}
func (c *Client) UpsertPoints(ctx context.Context, collection string, points []int) error {
	return nil
}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("transport-package function definition tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanUpsertPointsSoleOwner_TestFilesExempt verifies
// test files are exempt (residue documented in baseline).
func TestScanUpsertPointsSoleOwner_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "tests/fixtures/synthetic_upserter_test.go",
		`package fixtures
import "testing"
type C struct{}
func (c *C) TestUpsert(t *testing.T) {
	_ = c.UpsertPoints("x")
}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("test file tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanUpsertPointsSoleOwner_CommentOnlyResidue verifies
// comment-only references emit a WARN in !productionOnly mode.
func TestScanUpsertPointsSoleOwner_CommentOnlyResidue(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/application/random_other/docs.go",
		`package random_other
// client.UpsertPoints( is the canonical write surface.
func Note() {}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, false)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("comment-only produced violation: got %d, want 0", got)
	}
	if !containsString(rep.Warnings, "upsert-points-comments:") {
		t.Fatalf("comment-only did NOT produce WARN: %v", rep.Warnings)
	}
}

// TestScanUpsertPointsSoleOwner_ProductionOnlySilencesWarn
// verifies that productionOnly=true silences the comment-only
// WARN bucket.
func TestScanUpsertPointsSoleOwner_ProductionOnlySilencesWarn(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/application/random_other/docs.go",
		`package random_other
// client.UpsertPoints( is the canonical write surface.
func Note() {}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	for _, w := range rep.Warnings {
		if containsString([]string{w}, "upsert-points-comments:") {
			t.Fatalf("productionOnly did NOT silence comment-only WARN: %s", w)
		}
	}
	_ = strings.Join // silence unused-import if any
}

// TestScanUpsertPointsSoleOwner_DeletePointsNonCanonical verifies the
// destructive-twin gate (PR-HASH-SEMANTICS item 16, August 2026): a
// non-canonical `.DeletePoints(` call trips the same rule with the
// non_canonical_delete_points_caller MatchedRule.
func TestScanUpsertPointsSoleOwner_DeletePointsNonCanonical(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/application/random_other/bad_deleter.go",
		`package random_other
type C struct{}
func (c *C) Remove() {
	_ = c.DeletePoints("collection", []string{"a"})
}
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("non-canonical DeletePoints caller did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].MatchedRule != "non_canonical_delete_points_caller" {
		t.Fatalf("MatchedRule = %q, want non_canonical_delete_points_caller", rep.Violations[0].MatchedRule)
	}
}

// TestScanUpsertPointsSoleOwner_DeletePointsCanonicalExempt verifies the
// canonical projection-writer surface emitting `.DeletePoints(` is exempt.
func TestScanUpsertPointsSoleOwner_DeletePointsCanonicalExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForUpsertPointsTest(t, root, "internal/infrastructure/qdrant/indexing/projection_writer.go",
		`package indexing
type W struct{ client *C }
func (w *W) Delete() { _ = w.client.DeletePoints("c", []string{"a"}) }
`)
	rep := &report.Report{}
	ScanUpsertPointsSoleOwner(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("canonical DeletePoints caller surface tripped gate: %d violations", got)
	}
}
