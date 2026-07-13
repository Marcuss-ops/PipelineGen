// Package scan — test for ScanDirectIndexerBypassClosed
// (Card 7.2, July 2026).
//
// Hermetic (t.TempDir-anchored). Validates the four core
// invariants of the bypass-closure gate:
//
//  1. Production-code reference to one of the 7 deleted bypass
//     symbols trips the gate as SeverityError.
//  2. Test files (`_test.go`) are exempt — regression-guard
//     surface legitimately needs fixture setups.
//  3. Comment-only references (outside the documented residue
//     file set) emit a single WARN bucket in !productionOnly
//     mode; silenced in productionOnly mode.
//  4. The two documented residue files (outbox/dispatcher.go +
//     cmd/admin/reconcile_qdrant_adapters.go) emit a WARN
//     (NOT a violation) regardless of comment/production
//     status — they document the historical closure.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// makeFileForDirectIndexerBypassClosedTest writes a Go source
// file to <root>/<relPath> for hermetic per-check testing.
func makeFileForDirectIndexerBypassClosedTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanDirectIndexerBypassClosed_NonCanonicalTrip verifies
// that a production-code reference to ANY of the 7 deleted
// bypass symbols trips the gate as SeverityError.
//
// The count assertion is `>= len(directIndexerBypassClosedSymbols)`
// (not `==`) so a future Card who adds an 8th symbol does not
// break this test. The per-symbol check (each of the 7 known
// symbols is present in at least one violation note) locks the
// regex match to the canonical symbol set — a future agent who
// silently drops one from the regex (or replaces it with a
// different identifier) still fails the build.
//
// The symbol set is directIndexerBypassClosedSymbols (the
// package-level var in the percheck file) — single source of
// truth across regex + test.
func TestScanDirectIndexerBypassClosed_NonCanonicalTrip(t *testing.T) {
	root := t.TempDir()
	// All 7 symbols in one fixture (each line should produce a
	// separate violation).
	makeFileForDirectIndexerBypassClosedTest(t, root, "internal/random_other/reintroducer.go",
		`package random_other
import "fmt"
func ReintroduceTheBypass() {
	x := NewDirectIndexer()
	y := &DirectIndexer{}
	z := WithAdminReindex(nil)
	w := IsAdminReindex(nil)
	u := SetAdminReindexAuditLogger(nil)
	v := AdminReindexKey
	err := ErrDirectIndexerAbuse
	fmt.Println(x, y, z, w, u, v, err)
}
`)
	rep := &report.Report{}
	ScanDirectIndexerBypassClosed(root, nil, rep, true)
	if got := len(rep.Violations); got < len(directIndexerBypassClosedSymbols) {
		t.Fatalf("expected at least %d violations (one per deleted symbol), got %d\nfirst: %s",
			len(directIndexerBypassClosedSymbols), got, rep.Violations[0].Note)
	}
	for i, v := range rep.Violations {
		if v.Rule != directIndexerBypassClosedRule {
			t.Errorf("violation[%d].Rule = %q, want %q", i, v.Rule, directIndexerBypassClosedRule)
		}
		if v.Severity != string(report.SeverityError) {
			t.Errorf("violation[%d].Severity = %q, want %q", i, v.Severity, report.SeverityError)
		}
	}
	// Per-symbol lock: each of the 7 canonical symbols MUST appear
	// in at least one violation note. The match is via the
	// "matched: SYM" field in the note (NOT a bare substring),
	// because substring matching would conflate overlapping
	// symbols (e.g. "NewDirectIndexer" is a substring of
	// "NewDirectIndexer" itself, and "DirectIndexer" is a
	// substring of "NewDirectIndexer" — substring match would
	// pass for "DirectIndexer" off a "NewDirectIndexer" violation
	// note, masking a silent regex drift). The "matched: " prefix
	// is unconditional; the truncated snippet after it is
	// best-effort, so truncation cannot cause a false-positive
	// per-symbol check failure.
	for _, sym := range directIndexerBypassClosedSymbols {
		found := false
		for _, v := range rep.Violations {
			if strings.Contains(v.Note, "matched: "+sym) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected at least one violation to match symbol %q, none found in %d violations", sym, len(rep.Violations))
		}
	}
}

// TestScanDirectIndexerBypassClosed_TestFilesExempt verifies
// that `_test.go` files are exempt (regression-guard surface).
func TestScanDirectIndexerBypassClosed_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForDirectIndexerBypassClosedTest(t, root, "internal/random_other/reintroducer_test.go",
		`package random_other
import "testing"
func TestReintroduceTheBypass(t *testing.T) {
	x := NewDirectIndexer
	_ = x
	_ = DirectIndexer{}
	_ = WithAdminReindex(nil)
	_ = IsAdminReindex(nil)
	_ = SetAdminReindexAuditLogger(nil)
	_ = AdminReindexKey
	_ = ErrDirectIndexerAbuse
}
`)
	rep := &report.Report{}
	ScanDirectIndexerBypassClosed(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("test file tripped gate: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// TestScanDirectIndexerBypassClosed_CommentOnlyResidue
// verifies that a comment-only reference in a NON-residue file
// produces 0 violations + 1 WARN in !productionOnly mode.
func TestScanDirectIndexerBypassClosed_CommentOnlyResidue(t *testing.T) {
	root := t.TempDir()
	makeFileForDirectIndexerBypassClosedTest(t, root, "internal/random_other/docs_only.go",
		`package random_other
// DirectIndexer was removed in Card 7 (July 2026). We do NOT
// reintroduce it. See cmd/admin/reconcile_qdrant_adapters.go
// for the canonical admin reindex path (force=true seam).
func Note() {}
`)
	rep := &report.Report{}
	ScanDirectIndexerBypassClosed(root, nil, rep, false)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("comment-only reference produced violation: got %d, want 0", got)
	}
	if !containsAnyString(rep.Warnings, "bypass-symbol-residue:") {
		t.Fatalf("comment-only reference did NOT produce WARN: %v", rep.Warnings)
	}
}

// TestScanDirectIndexerBypassClosed_ProductionOnlySilencesWarn
// verifies that productionOnly=true silences the comment-only
// WARN bucket (operator-facing zero-hits claim).
func TestScanDirectIndexerBypassClosed_ProductionOnlySilencesWarn(t *testing.T) {
	root := t.TempDir()
	makeFileForDirectIndexerBypassClosedTest(t, root, "internal/random_other/docs_only.go",
		`package random_other
// DirectIndexer was removed in Card 7 (July 2026). Do not reintroduce.
func Note() {}
`)
	rep := &report.Report{}
	ScanDirectIndexerBypassClosed(root, nil, rep, true)
	for _, w := range rep.Warnings {
		if containsAnyString([]string{w}, "bypass-symbol-residue:") {
			t.Fatalf("productionOnly mode did NOT silence comment-only WARN: %s", w)
		}
	}
}

// TestScanDirectIndexerBypassClosed_ResidueFilesEmitWARN
// verifies that the two documented residue files (outbox/
// dispatcher.go + cmd/admin/reconcile_qdrant_adapters.go)
// emit a WARN, NOT a violation, even when the reference is
// on a non-comment line.
//
// The test is table-driven across (residue file × productionOnly
// mode) so both documented residue paths are covered AND both
// productionOnly modes are pinned. The residue-as-WARN contract
// is orthogonal to the productionOnly flag: the WARN is emitted
// in dev mode (so the operator sees the documented residue) and
// silenced in production mode (so the operator-facing "zero
// production-code hits" claim stays auditable).
func TestScanDirectIndexerBypassClosed_ResidueFilesEmitWARN(t *testing.T) {
	cases := []struct {
		name           string
		relPath        string
		productionOnly bool
		wantWARN       bool
	}{
		{
			name:           "dispatcher_residue_dev_mode_emits_WARN",
			relPath:        "internal/infrastructure/database/sqlite/outbox/dispatcher.go",
			productionOnly: false,
			wantWARN:       true,
		},
		{
			name:           "reconcile_adapters_residue_dev_mode_emits_WARN",
			relPath:        "cmd/admin/reconcile_qdrant_adapters.go",
			productionOnly: false,
			wantWARN:       true,
		},
		{
			name:           "dispatcher_residue_prod_mode_silenced",
			relPath:        "internal/infrastructure/database/sqlite/outbox/dispatcher.go",
			productionOnly: true,
			wantWARN:       false,
		},
		{
			name:           "reconcile_adapters_residue_prod_mode_silenced",
			relPath:        "cmd/admin/reconcile_qdrant_adapters.go",
			productionOnly: true,
			wantWARN:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// Mirror the actual residue file shape: a non-comment
			// production-style line referencing one of the 7 deleted
			// symbols, in the documented residue file path.
			makeFileForDirectIndexerBypassClosedTest(t, root, tc.relPath,
				`package residue_fixture
// (DirectIndexer) was removed in Card 7 (July 2026) — documented residue.
var x = NewDirectIndexer
type Dispatcher struct{}
`)
			rep := &report.Report{}
			ScanDirectIndexerBypassClosed(root, nil, rep, tc.productionOnly)
			if got := len(rep.Violations); got != 0 {
				t.Fatalf("residue file produced violation: got %d, want 0\nfirst: %s",
					got, rep.Violations[0].Note)
			}
			gotWARN := containsAnyString(rep.Warnings, "bypass-symbol-residue:")
			if gotWARN != tc.wantWARN {
				t.Fatalf("residue WARN presence = %v, want %v (full warnings: %v)",
					gotWARN, tc.wantWARN, rep.Warnings)
			}
		})
	}
}

// TestScanDirectIndexerBypassClosed_WordBoundaryNoFalsePositive
// verifies that a SUB-STRING reference (e.g. an identifier
// like "NewDirectIndexerFactory" or "IsAdminReindexEnabled")
// does NOT trip the gate — the word-boundary regex requires
// the symbol to be a standalone token.
func TestScanDirectIndexerBypassClosed_WordBoundaryNoFalsePositive(t *testing.T) {
	root := t.TempDir()
	makeFileForDirectIndexerBypassClosedTest(t, root, "internal/random_other/substring.go",
		`package random_other
// These are all NEW identifiers that happen to contain the
// deleted symbols as sub-strings. The word-boundary regex
// MUST NOT trip on them.
var NewDirectIndexerFactory = 1
var IsAdminReindexEnabled = 2
var DirectIndexersAreBad = 3
var SetAdminReindexAuditLoggerV2 = 4
`)
	rep := &report.Report{}
	ScanDirectIndexerBypassClosed(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("word-boundary false positive: got %d violations\nfirst: %s",
			got, rep.Violations[0].Note)
	}
}

// containsAnyString returns true if any haystack contains
// the needle. Mirrors the helper in percheck_asset_committer_event_ssot_test.go.
func containsAnyString(haystacks []string, needle string) bool {
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
