// Package scan — companion test for percheck_finalizer_no_direct_sql.go.
//
// Pins:
//
//	(a) "legacy SQL trip" — a non-canonical, non-test file in the
//	    finalizer package that contains `INSERT INTO media_assets`
//	    (or asset_locations / outbox_events) emits a violation.
//	(b) "canonical committer is exempt" — the canonical
//	    internal/platform/sqlite/assets/asset_committer.go
//	    is exempt (it IS the SSOT).
//	(c) "comment-only is residue-accounted" — a comment-only line
//	    that mentions the forbidden table names does NOT emit a
//	    violation; it is WARNed (residue accounting per godlike/07).
package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanFinalizerNoDirectSQL_LegacySQLTrip verifies that a
// non-canonical, non-test file in the finalizer package that
// contains a direct SQL write to media_assets emits a
// violation. The test creates a temp directory tree mirroring
// the canonical layout, writes a synthetic "legacy" file with
// the forbidden pattern, and asserts that the scan flags it.
func TestScanFinalizerNoDirectSQL_LegacySQLTrip(t *testing.T) {
	tmp := t.TempDir()
	// Mirror the finalizer package layout.
	finalizerDir := filepath.Join(tmp, "internal/application/assets/finalizer")
	if err := os.MkdirAll(finalizerDir, 0o755); err != nil {
		t.Fatalf("mkdir finalizer: %v", err)
	}
	legacyFile := filepath.Join(finalizerDir, "asset_finalizer_legacy_test_target.go")
	legacyBody := `package finalizer

// LegacySQLProbe is a probe struct used by the archcheck test.
// It deliberately contains a forbidden SQL write to trip the gate.
type LegacySQLProbe struct{}

func (LegacySQLProbe) Probe() string {
	_ = ` + "`INSERT INTO media_assets (id, source) VALUES ('x', 'y')`" + `
	return "probe"
}
`
	if err := os.WriteFile(legacyFile, []byte(legacyBody), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	r := &report.Report{}
	ScanFinalizerNoDirectSQL(tmp, &policy.Policy{}, r)

	if len(r.Violations) == 0 {
		t.Fatalf("expected at least one violation for legacy SQL trip; got 0")
	}
	// Find the violation for our probe file.
	found := false
	for _, v := range r.Violations {
		if v.Rule == finalizerNoSQLRule && v.File == "internal/application/assets/finalizer/asset_finalizer_legacy_test_target.go" {
			found = true
			if v.MatchedRule != "forbidden_sql_media_assets" {
				t.Errorf("expected MatchedRule=forbidden_sql_media_assets; got %q", v.MatchedRule)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected violation for probe file; got violations=%+v", r.Violations)
	}
}

// TestScanFinalizerNoDirectSQL_CanonicalCommitterExempt verifies
// that the canonical AssetCommitter file is exempt from the gate
// (it IS the SSOT). The test creates a temp tree, writes a
// synthetic file at the canonical path with a forbidden SQL
// pattern, and asserts that the scan does NOT emit a violation
// for that file.
func TestScanFinalizerNoDirectSQL_CanonicalCommitterExempt(t *testing.T) {
	tmp := t.TempDir()
	canonicalDir := filepath.Join(tmp, "internal/platform/sqlite/assets")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("mkdir canonical: %v", err)
	}
	canonicalFile := filepath.Join(canonicalDir, "asset_committer.go")
	canonicalBody := `package assets

// CanonicalAssetCommitterProbe is a probe struct that mirrors the
// canonical SOLE owner of the asset commit SQL.
type CanonicalAssetCommitterProbe struct{}

func (CanonicalAssetCommitterProbe) Commit() string {
	_ = ` + "`INSERT INTO media_assets (id) VALUES ('x')`" + `
	return "canonical"
}
`
	if err := os.WriteFile(canonicalFile, []byte(canonicalBody), 0o644); err != nil {
		t.Fatalf("write canonical file: %v", err)
	}

	r := &report.Report{}
	ScanFinalizerNoDirectSQL(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.File == "internal/platform/sqlite/assets/asset_committer.go" {
			t.Errorf("canonical AssetCommitter file should be exempt; got violation=%+v", v)
		}
	}
}

// TestScanFinalizerNoDirectSQL_CommentOnlyIsResidueAccounted
// verifies that a comment-only line that mentions a forbidden
// table name does NOT emit a violation; it is WARNed (residue
// accounting per godlike/07).
func TestScanFinalizerNoDirectSQL_CommentOnlyIsResidueAccounted(t *testing.T) {
	tmp := t.TempDir()
	finalizerDir := filepath.Join(tmp, "internal/application/assets/finalizer")
	if err := os.MkdirAll(finalizerDir, 0o755); err != nil {
		t.Fatalf("mkdir finalizer: %v", err)
	}
	commentFile := filepath.Join(finalizerDir, "asset_finalizer_comment_test_target.go")
	commentBody := `package finalizer

// CommentOnlyProbe is a probe struct whose docstring mentions
// the forbidden table names. The line below is a comment-only
// reference to INSERT INTO media_assets and MUST be detected as
// residue accounting (godlike/07) by the scan. The scan emits a
// WARN, NOT a violation, for this case.
type CommentOnlyProbe struct{}
`
	if err := os.WriteFile(commentFile, []byte(commentBody), 0o644); err != nil {
		t.Fatalf("write comment file: %v", err)
	}

	r := &report.Report{}
	ScanFinalizerNoDirectSQL(tmp, &policy.Policy{}, r)

	// The comment mentions "INSERT INTO media_assets" inside a
	// `//` block. The scan should detect it as residue and WARN,
	// not VIOLATE. Verify no violation for the comment file.
	for _, v := range r.Violations {
		if v.File == "internal/application/assets/finalizer/asset_finalizer_comment_test_target.go" {
			t.Errorf("comment-only line should not emit a violation; got=%+v", v)
		}
	}
	// Verify the WARN bucket captured the residue.
	foundWarn := false
	for _, w := range r.Warnings {
		if len(w) > 0 && w == finalizerNoSQLRule+" forbidden-sql: comment-only reference(s) to forbidden SQL patterns in internal/application/assets/finalizer/asset_finalizer_comment_test_target.go (descriptive prose; non-fatal per godlike/07 no-fake-availability)" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected residue-accounting WARN for comment-only line; got warnings=%+v", r.Warnings)
	}
}
