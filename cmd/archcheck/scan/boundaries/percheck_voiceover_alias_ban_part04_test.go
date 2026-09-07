package boundaries

import (
	"testing"
)

// Per the percheck_root_override.go extension of the percheck_player_client
// design: the operator-facing "zero production-code hits" claim
// is auditable via `len(r.Violations) == 0`. The productionOnly
// flag silences ONLY comment-only Warnings — production-code
// Violations STILL fire regardless of the flag.
//
// This test pins the canonical behavior: productionOnly=true
// does NOT change the Violation count (production-code hits are
// always flagged). The flag's only effect is on Warnings
// (residue-accounting for comment-only lines is silenced in
// productionOnly mode for a cleaner operator dashboard).
//
// This lock prevents a future regression that over-applies
// productionOnly (the operator's "I want a clean reading of
// production-code-only hits" query mode must NOT swallow real
// production-code Violations).
func TestScanVoiceoverAliasBan_ProductionOnlyPreservesViolations(t *testing.T) {
	root := t.TempDir()

	// Fixture: BOTH a production-code reference AND a comment-only
	// reference to the retired alias in the same file. The
	// productionOnly=true mode MUST:
	//   - preserve the production-code Violation (1 Violation)
	//   - SILENCE the comment-only Warning (0 Warnings)
	// This is the asymmetric behavior that makes productionOnly
	// useful for the operator-facing "clean reading" use case.
	//
	// The comment line intentionally contains the alias literal
	// (voiceover.VoiceoverRecord) so the 0-Warnings assertion is
	// LOAD-BEARING: without the comment-only reference, the
	// assertion would pass trivially (0 alerts because 0 comment
	// matches). With the reference, the assertion verifies
	// productionOnly ACTUALLY silences the comment-only detection.
	writeFixtureAliasBan(t, root, "internal/capabilities/voiceover/service_smuggle/prodcodeprod.go", `package voiceover_smuggle

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"

// Legacy migration comment: voiceover.VoiceoverRecord (canonical: persistence.VoiceoverRecord)
// MUST be SILENCED in productionOnly mode (residue accounting suppressed).
type Spec struct{ Name string }

// Production-code line: var _ = voiceover.VoiceoverRecord
// MUST be PRESERVED as Violation regardless of productionOnly flag.
var _ = voiceover.VoiceoverRecord
`)

	r := newEmptyReportAliasBan()
	ScanVoiceoverAliasBan(root, newTestPolicyAliasBan(), r, true /* productionOnly */)

	// 1 violation: the production-code line is ALWAYS flagged,
	// regardless of the productionOnly flag. The flag is
	// asymmetric: it does NOT change Violation accounting.
	if got := len(r.Violations); got != 1 {
		t.Fatalf("expected exactly 1 violation (production-code reference, productionOnly PRESERVES violations), got %d: %+v", got, r.Violations)
	}
	// 0 warnings: the comment-only line is SILENCED in
	// productionOnly mode (residue accounting NOT emitted).
	// This assertion is LOAD-BEARING — the comment line above
	// contains the alias literal; without the productionOnly
	// flag silencing, the residue-accounting would surface
	// >= 1 Warning. Inverse assertion verified by Test 4
	// (TestScanVoiceoverAliasBan_CommentOnlyWarned).
	if got := len(r.Warnings); got != 0 {
		t.Fatalf("expected 0 warnings (comment-only line SILENCED in productionOnly mode), got %d: %+v", got, r.Warnings)
	}
}
