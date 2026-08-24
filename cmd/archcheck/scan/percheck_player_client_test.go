// Package scan — hermetic TDD coverage for
// percheck_player_client_centralization.
//
// percheck_player_client_test.go exercises the Check N
// forward-prevention gate (PR-PLAYER-CLIENT-DRIFT-FIX,
// 2026-07-06) via synthetic file fixtures in t.TempDir. The
// tests are decoupled from the real repo state so a future
// refactor of the gate's exclusion list surfaces as a test
// failure here BEFORE it would land in production.
//
// Coverage matrix:
//
//  1. Canonical file is NOT flagged (the literal MUST live
//     here per godlike/06 SSOT).
//  2. Test files are NOT flagged (regression guards
//     legitimately reference the literal for invariant
//     pinning; excluding tests prevents false positives).
//  3. Production file containing the drift IS flagged with
//     the canonical violation shape (error severity +
//     percheck_player_client_centralization rule +
//     matched_rule + Note referencing the canonical SSOT +
//     PR-PLAYER-CLIENT-DRIFT-FIX).
//  4. Comment-only hits are WARN'd, NOT violation (godlike/07
//     no-fake-availability residue accounting).
//  5. Clean production file (no literal anywhere) is NOT
//     flagged.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFixture writes a synthetic .go file at the given
// repo-relative path inside root, creating parent directories
// as needed. Returns the absolute file path. Used to build
// the per-test file layout deterministically.
func writeFixture(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", abs, err)
	}
	return abs
}

// newTestPolicy returns an empty policy. The Check N scanner
// does not consult the policy struct (it walks the repo + the
// canonical SSOT path is hard-coded), so an empty policy is
// sufficient. Future policy-driven tuning (e.g. allowlist
// rows for sites that legitimately need the literal) would
// thread the policy here.
func newTestPolicy() *policy.Policy {
	return &policy.Policy{}
}

// newEmptyReport returns a freshly-initialised Report. The
// Summary.ByReason + BySeverity maps MUST be non-nil so the
// runner's rollup step is safe even on a zero-violation run.
func newEmptyReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

// TestScanPlayerClientCentralization_CanonicalFileIsNotFlagged is
// the load-bearing test: the canonical SSOT file MUST contain
// the literal without the gate flagging it. If this test
// fails, the gate has over-zealously excluded the canonical
// owner and the centralization contract is broken.
func TestScanPlayerClientCentralization_CanonicalFileIsNotFlagged(t *testing.T) {
	root := t.TempDir()
	canonicalContent := `package ytdlp

// Canonical centralization of the player_client literal.
// This is the SOLE owner of the web,android policy.
const canonicalExtractorArg = "youtube:player_client=web,android"
`
	writeFixture(t, root, playerClientCanonicalRelPath, canonicalContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("canonical file MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanPlayerClientCentralization_TestFilesAreNotFlagged
// pins the regression-guard exemption. Test files
// legitimately reference the literal for invariant pinning
// (cmd_builder_test.go pins the canonical value +
// NeverWebOnly regression; metadata_test.go has 3 new tests
// that pin the canonical web,android order).
func TestScanPlayerClientCentralization_TestFilesAreNotFlagged(t *testing.T) {
	root := t.TempDir()
	// Synthetic test file referencing the literal — should
	// NOT be flagged.
	testContent := `package youtube

import "testing"

func TestRegression(t *testing.T) {
	want := "youtube:player_client=web,android"
	if want != "youtube:player_client=web,android" {
		t.Fatal("drift detected")
	}
}
`
	writeFixture(t, root, "internal/infrastructure/youtube/metadata_test.go", testContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("test files MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanPlayerClientCentralization_ProductionFileIsFlagged is
// the load-bearing forward-prevention test: a production
// .go file containing the literal MUST be flagged. This
// catches future drift like the PR-PLAYER-CLIENT-DRIFT-FIX
// pre-PR state where metadata.go:95 had the reversed-order
// literal.
func TestScanPlayerClientCentralization_ProductionFileIsFlagged(t *testing.T) {
	root := t.TempDir()
	driftContent := `package videopipeline

// Some other production file that re-declares the literal
// (simulating the pre-PR-PLAYER-CLIENT-DRIFT-FIX drift).
const extractorArg = "youtube:player_client=android,web"
`
	relPath := "internal/infrastructure/someother/videopipeline.go"
	writeFixture(t, root, relPath, driftContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("production file with drift MUST be flagged exactly once; got %d violation(s): %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_player_client_centralization" {
		t.Errorf("violation rule = %q, want percheck_player_client_centralization", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("violation severity = %q, want error (forward-prevention gate)", v.Severity)
	}
	if v.MatchedRule != "player_client_centralization_gate" {
		t.Errorf("violation matched_rule = %q, want player_client_centralization_gate", v.MatchedRule)
	}
	if !strings.Contains(v.Note, playerClientCanonicalRelPath) {
		t.Errorf("violation Note must reference the canonical SSOT file %q; got: %s", playerClientCanonicalRelPath, v.Note)
	}
	if !strings.Contains(v.Note, "PR-PLAYER-CLIENT-DRIFT-FIX") {
		t.Errorf("violation Note must reference PR-PLAYER-CLIENT-DRIFT-FIX for historical context; got: %s", v.Note)
	}
}

// TestScanPlayerClientCentralization_CommentOnlyIsWarned pins
// the godlike/07 no-fake-availability residue-accounting
// behaviour: full-line `//`-prefixed comments that mention
// the literal are NOT surfaced as violations (descriptive
// prose, not a real re-declaration) but ARE logged as
// warnings so future drift is visible in CI output every
// run.
func TestScanPlayerClientCentralization_CommentOnlyIsWarned(t *testing.T) {
	root := t.TempDir()
	commentOnlyContent := `package somepackage

// Note: the canonical player_client= policy is centralized
// in internal/platform/ytdlp/cmd_builder.go per
// godlike/06 SSOT. See PR-PLAYER-CLIENT-DRIFT-FIX.
//
// This file MUST NOT re-declare the literal.
const someUnrelatedValue = 42
`
	relPath := "internal/infrastructure/someother/comments.go"
	writeFixture(t, root, relPath, commentOnlyContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("comment-only hits MUST NOT be flagged as violations; got %d: %+v", got, r.Violations)
	}
	// Warn should mention the comment count.
	foundWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "comment-only") && strings.Contains(w, relPath) {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected a warning mentioning the comment-only hit in %s; got warnings: %+v", relPath, r.Warnings)
	}
}

// TestScanPlayerClientCentralization_CleanFileIsNotFlagged is
// the negative baseline: a production .go file with no
// literal anywhere must NOT be flagged.
func TestScanPlayerClientCentralization_CleanFileIsNotFlagged(t *testing.T) {
	root := t.TempDir()
	cleanContent := `package somepackage

import "context"

type Service struct{}

func (s *Service) Do(ctx context.Context) error {
	return nil
}
`
	writeFixture(t, root, "internal/infrastructure/someother/clean.go", cleanContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean file MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("clean file MUST NOT generate warnings; got %d: %+v", got, r.Warnings)
	}
}

// TestScanPlayerClientCentralization_ScannerFileIsNotFlagged
// pins the self-exemption contract (round-2 review): the
// archcheck scanner directory (cmd/archcheck/scan/) legitimately
// contains the literal as the search target (the
// `playerClientLiteral` const) and as part of the violation
// Note. Without this exemption, the gate would fail-closed on
// its own source code (`--strict` mode exit 1). This test locks
// the exemption so a future agent who naively tightens the
// exclusion list surfaces as a test failure here.
func TestScanPlayerClientCentralization_ScannerFileIsNotFlagged(t *testing.T) {
	root := t.TempDir()
	// Simulate the scanner self-flag scenario: a file under
	// the cmd/archcheck/scan/ directory containing the
	// literal in a const declaration (the very pattern that
	// would otherwise fail-closed on the gate itself).
	scannerFileContent := "package scan\n\n" +
		"const playerClientLiteral = \"player_client=\"\n\n" +
		"const playerClientScanNote = \"forbidden `player_client=` literal outside canonical SSOT\"\n"
	relPath := "cmd/archcheck/scan/some_hypothetical_check.go"
	writeFixture(t, root, relPath, scannerFileContent)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("scanner directory files MUST be exempt from the gate (self-exemption contract); got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanPlayerClientCentralization_MixedRepoLayout exercises
// the realistic case: a synthetic repo with the canonical
// file, a test file, a clean production file, a drift
// production file, and a comment-only file. Only the drift
// production file should be flagged; the canonical + test +
// clean files should be silent; the comment-only file
// should generate a warning.
func TestScanPlayerClientCentralization_MixedRepoLayout(t *testing.T) {
	root := t.TempDir()

	// (1) Canonical SSOT
	writeFixture(t, root, playerClientCanonicalRelPath, `package ytdlp
const x = "youtube:player_client=web,android"
`)

	// (2) Regression-guard test file
	writeFixture(t, root, "internal/infrastructure/youtube/metadata_test.go", `package youtube
import "testing"
func TestX(t *testing.T) { _ = "youtube:player_client=web,android" }
`)

	// (3) Clean production file
	writeFixture(t, root, "internal/infrastructure/clean/clean.go", `package clean
func Do() {}
`)

	// (4) Drift production file (THE violation)
	driftRelPath := "internal/infrastructure/drift/drift.go"
	writeFixture(t, root, driftRelPath, `package drift
const x = "youtube:player_client=android,web"
`)

	// (5) Comment-only file (warning, not violation)
	commentRelPath := "internal/infrastructure/comments/comments.go"
	writeFixture(t, root, commentRelPath, `package comments
// The canonical player_client= literal lives elsewhere.
`)

	r := newEmptyReport()
	ScanPlayerClientCentralization(root, newTestPolicy(), r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("mixed layout MUST surface exactly 1 violation (the drift file); got %d: %+v", got, r.Violations)
	}
	if r.Violations[0].File != driftRelPath {
		t.Errorf("violation file = %q, want %q", r.Violations[0].File, driftRelPath)
	}

	// The comment-only file should appear in Warnings.
	foundCommentWarn := false
	for _, w := range r.Warnings {
		if strings.Contains(w, commentRelPath) && strings.Contains(w, "comment-only") {
			foundCommentWarn = true
			break
		}
	}
	if !foundCommentWarn {
		t.Errorf("expected a comment-only warning for %s; got warnings: %+v", commentRelPath, r.Warnings)
	}
}
