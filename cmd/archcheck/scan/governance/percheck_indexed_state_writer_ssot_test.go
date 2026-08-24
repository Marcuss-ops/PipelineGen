// Package scan — percheck_indexed_state_writer_ssot_test.go
// (Wave YY, INDEXED state SSOT forward-prevention, July 2026)
//
// Pins the forward-prevention scanner for the godlike/06 SSOT
// contract: media_assets.index_state='INDEXED' transitions ONLY
// via the canonical outbox consumer (IndexingHandler →
// clipindexer.IndexClip → setIndexedAt).
//
// godlike/07 fail-fast: the tests use synthetic .go files inside
// t.TempDir() — no production files are touched at test time.
// The scanner's output for each scenario is asserted against an
// empty Report seeded in each test.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFakeIndexedStateWriterViolation writes a synthetic Go file
// inside internal/application/** (NOT the canonical clipindexer
// path) that contains an SQL write to index_state='INDEXED'. The
// fixture filename is `dirty_indexed_state_writer.go` so the
// forward-prevention assertion maps to the user's literal scenario
// (a workflow bypassing the outbox consumer).
func writeFakeIndexedStateWriterViolation(t *testing.T, tempDir, fixturePath string) string {
	t.Helper()
	dir := filepath.Join(tempDir, filepath.Dir(fixturePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	path := filepath.Join(tempDir, fixturePath)
	body := "package images\n\n" +
		"// dirty_indexed_state_writer.go: violation fixture —\n" +
		"// writing media_assets.index_state='INDEXED' from a workflow\n" +
		"// (internal/capabilities/images/workflow/) is FORBIDDEN. The canonical\n" +
		"// INDEXED state transition is via the outbox consumer\n" +
		"// (IndexingHandler → IndexClip → setIndexedAt).\n" +
		"func dirty() error {\n" +
		"\t_, err := db.Exec(`UPDATE media_assets SET index_state = 'INDEXED' WHERE id = ?`, id)\n" +
		"\treturn err\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write violation file: %v", err)
	}
	return path
}

// writeFakeIndexedStateWriterCanonical writes a synthetic Go file
// inside the canonical clipindexer package
// (internal/infrastructure/indexing/clipindexer/) that contains an
// SQL write to index_state='INDEXED'. The canonical writer —
// must NOT trip.
func writeFakeIndexedStateWriterCanonical(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "infrastructure", "indexing", "clipindexer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir canonical dir: %v", err)
	}
	path := filepath.Join(dir, "set_indexed_at.go")
	body := "package clipindexer\n\n" +
		"// set_indexed_at.go: the canonical INDEXED writer — must\n" +
		"// NOT trip the percheck (canonical package exempt).\n" +
		"func canonicalSetIndexedAt() error {\n" +
		"\t_, err := db.Exec(`UPDATE media_assets SET index_state = 'INDEXED' WHERE id = ? AND source_version = ? AND index_state = 'INDEXING'`, id, sv)\n" +
		"\treturn err\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write canonical file: %v", err)
	}
	return path
}

// writeFakeIndexedStateWriterTestExempt writes a synthetic _test.go
// file inside internal/application/** that contains an SQL write
// to index_state='INDEXED'. _test.go is exempt — must NOT trip.
func writeFakeIndexedStateWriterTestExempt(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}
	path := filepath.Join(dir, "fixture_indexed_writer_test_exempt_test.go")
	body := "package images\n\n" +
		"// Test fixture: the regression-guard allowlist writes\n" +
		"// index_state='INDEXED' to simulate the worker's behavior\n" +
		"// for end-to-end tests. _test.go suffix is exempt from\n" +
		"// the percheck gate.\n" +
		"func fixtureSetIndexed() error {\n" +
		"\t_, err := db.Exec(`UPDATE media_assets SET index_state = 'INDEXED' WHERE id = ?`, id)\n" +
		"\treturn err\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write test-exempt file: %v", err)
	}
	return path
}

// writeFakeIndexedStateWriterCommentOnly writes a synthetic file
// inside internal/application/** that ONLY references the SQL
// pattern in comments. Residue accounting (godlike/07) — must
// warn, not violate.
func writeFakeIndexedStateWriterCommentOnly(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "clips")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir comment-only dir: %v", err)
	}
	path := filepath.Join(dir, "doc_residue.go")
	body := "package clips\n\n" +
		"// doc_residue.go: ONLY references the SQL pattern in\n" +
		"// comments. Residue accounting discipline (godlike/07) —\n" +
		"// descriptive prose is non-fatal but MUST be flagged as\n" +
		"// a WARN.\n" +
		"//\n" +
		"// NOTE: a workflow MUST NOT write\n" +
		"// `index_state = 'INDEXED'` directly. The canonical path\n" +
		"// is the outbox consumer (IndexingHandler → IndexClip →\n" +
		"// setIndexedAt).\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write comment-only file: %v", err)
	}
	return path
}

// writeFakeIndexedStateWriterScopeMarker writes a synthetic file
// inside internal/application/** that contains the SQL pattern
// AND the INDEXED_WRITER_SCOPE comment marker. The marker exempts
// the file (documented allowlist for edge cases).
func writeFakeIndexedStateWriterScopeMarker(t *testing.T, tempDir string) string {
	t.Helper()
	dir := filepath.Join(tempDir, "internal", "application", "reconcile")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir scope-marker dir: %v", err)
	}
	path := filepath.Join(dir, "reconcile_stale_indexed.go")
	body := "package reconcile\n\n" +
		"// INDEXED_WRITER_SCOPE: clipindexer\n" +
		"//\n" +
		"// reconcile_stale_indexed.go: future admin-tool reconcile\n" +
		"// path that re-stamps a stale INDEXED row. Uses the\n" +
		"// comment-marker allowlist to document the scope.\n" +
		"func reconcileStaleIndexed() error {\n" +
		"\t_, err := db.Exec(`UPDATE media_assets SET index_state = 'INDEXED' WHERE id = ?`, id)\n" +
		"\treturn err\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write scope-marker file: %v", err)
	}
	return path
}

// TestScanIndexedStateWriterSSOT_OnlyCanonicalPasses verifies the
// happy path: only the canonical clipindexer package + a scope-
// marker file write INDEXED, so zero violations are emitted.
func TestScanIndexedStateWriterSSOT_OnlyCanonicalPasses(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeIndexedStateWriterCanonical(t, tempDir)
	writeFakeIndexedStateWriterScopeMarker(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule {
			t.Errorf("expected zero violations when only canonical + scope-marker writes; got rule=%s line=%d note=%s",
				v.Rule, v.Line, v.Note)
		}
	}
}

// TestScanIndexedStateWriterSSOT_RuleIdStable pins the canonical
// percheck_indexed_state_writer_ssot rule id so a future rename
// surfaces as a loud test failure.
func TestScanIndexedStateWriterSSOT_RuleIdStable(t *testing.T) {
	const want = "percheck_indexed_state_writer_ssot"
	if indexedStateWriterSSOTRule != want {
		t.Errorf("indexedStateWriterSSOTRule = %q, want %q (runner.go CheckSpec.Name lockstep)",
			indexedStateWriterSSOTRule, want)
	}
}

// TestScanIndexedStateWriterSSOT_DirtyApplicationFails is the
// load-bearing forward-prevention assertion: a non-canonical
// file inside internal/application/** that writes
// index_state='INDEXED' MUST trip the gate with exactly one
// violation.
func TestScanIndexedStateWriterSSOT_DirtyApplicationFails(t *testing.T) {
	tempDir := t.TempDir()
	violatingPath := writeFakeIndexedStateWriterViolation(t, tempDir,
		"internal/capabilities/images/workflow/dirty_indexed_state_writer.go")

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule &&
			strings.HasSuffix(v.File, "dirty_indexed_state_writer.go") {
			found++
			if v.MatchedRule != "indexed_state_writer_ssot" {
				t.Errorf("MatchedRule = %q, want indexed_state_writer_ssot", v.MatchedRule)
			}
			if !strings.Contains(v.Note, "forbidden") {
				t.Errorf("Note must include 'forbidden'; got %q", v.Note)
			}
			if !strings.Contains(v.Note, "IndexingHandler") {
				t.Errorf("Note must include 'IndexingHandler' canonical-path reference; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation on %s; got %d (all violations: %d)",
			violatingPath, found, len(r.Violations))
	}
}

// TestScanIndexedStateWriterSSOT_CanonicalExempted verifies the
// canonical writer package is exempt: a file at
// internal/infrastructure/indexing/clipindexer/** that writes
// index_state='INDEXED' MUST NOT trip the gate.
func TestScanIndexedStateWriterSSOT_CanonicalExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeIndexedStateWriterCanonical(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule &&
			strings.Contains(v.File, "set_indexed_at.go") {
			t.Errorf("canonical clipindexer package MUST be exempt; got violation: %s", v.Note)
		}
	}
}

// TestScanIndexedStateWriterSSOT_TestFileExempted verifies the
// _test.go regression-guard allowlist: a test file inside
// internal/application/** that writes index_state='INDEXED'
// MUST NOT trip the gate.
func TestScanIndexedStateWriterSSOT_TestFileExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeIndexedStateWriterTestExempt(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule &&
			strings.HasSuffix(v.File, "_test.go") {
			t.Errorf("test file MUST be exempt; got violation: %s", v.Note)
		}
	}
}

// TestScanIndexedStateWriterSSOT_ReadProjectionIsNotAWriter verifies
// that a qualified predicate in a SELECT-style projection is not
// mistaken for a state transition. The operatorread projection is
// allowed to describe INDEXED because it never assigns the column.
func TestScanIndexedStateWriterSSOT_ReadProjectionIsNotAWriter(t *testing.T) {
	tempDir := t.TempDir()
	dir := filepath.Join(tempDir, "internal", "infrastructure", "database", "sqlite", "assets", "operatorread")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir projection dir: %v", err)
	}
	path := filepath.Join(dir, "state_projection.go")
	body := "package operatorread\\n\\n" +
		"func projection(alias string) string {\\n" +
		"\\treturn \"CASE WHEN \" + alias + \".index_state = 'INDEXED' THEN 'INDEXED' END\"\\n" +
		"}\\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write projection fixture: %v", err)
	}

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule {
			t.Errorf("read-only projection MUST NOT trip writer check; got %s:%d %s", v.File, v.Line, v.Note)
		}
	}
}

// TestScanIndexedStateWriterSSOT_CommentOnlyIsResidue verifies
// the godlike/07 residue accounting discipline: a comment-only
// reference to the SQL pattern yields a WARN, not a violation.
func TestScanIndexedStateWriterSSOT_CommentOnlyIsResidue(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeIndexedStateWriterCommentOnly(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule &&
			strings.HasSuffix(v.File, "doc_residue.go") {
			t.Errorf("comment-only references must NOT trip violation; got: %s", v.Note)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, indexedStateWriterSSOTRule) &&
			strings.Contains(w, "doc_residue.go") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected residue warn on doc_residue.go; r.Warnings did not contain it (warnings=%v)", r.Warnings)
	}
}

// TestScanIndexedStateWriterSSOT_ScopeMarkerExempted verifies
// the comment-marker allowlist: a file with
// `// INDEXED_WRITER_SCOPE: clipindexer` in its header that
// writes index_state='INDEXED' MUST NOT trip the gate.
func TestScanIndexedStateWriterSSOT_ScopeMarkerExempted(t *testing.T) {
	tempDir := t.TempDir()
	writeFakeIndexedStateWriterScopeMarker(t, tempDir)

	r := &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
	ScanIndexedStateWriterSSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == indexedStateWriterSSOTRule &&
			strings.Contains(v.File, "reconcile_stale_indexed.go") {
			t.Errorf("scope-marker file MUST be exempt; got violation: %s", v.Note)
		}
	}
}
