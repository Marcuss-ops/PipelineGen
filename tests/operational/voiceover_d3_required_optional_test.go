// Package operational — TestVoiceoverD3RequiredOptional is the FASE D3
// gate: 2 sub-cases that exercise the required/optional failure semantics
// of the voiceover ParentAggregator.
//
//	D3a — required-fail: 1 required item with FAKE voice → child FAILED
//	      → parent state=failed + broker=FAILED (Transition() rule ①
//	      short-circuit to FailedTerminal).
//	D3b — optional-fail: 2 items (1 required valid + 1 optional FAKE)
//	      → 1 SUCCEEDED + 1 FAILED → parent state=partial_success +
//	      broker=SUCCEEDED (optional failure tolerated per FASE 1 Compute()
//	      semantics + FASE 2 voiceover.ParentPartialSuccess mapping).
//
// 8 assertions total: 4 per sub-case (1 child status + 1 final state +
// 1 typed column + 1 broker status).
//
// Honest-limitation: the FAKE voice name relies on edge_tts rejecting
// the voice. If the TTS server falls back to a default voice on invalid
// input, the assertions would fail (the test would observe SUCCEEDED
// children instead of FAILED). Verify on your worker version.
//
// Pure-stdlib wrapper (no internal/* imports) so it compiles cleanly even
// with the 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skipped when:
//   - go test -short is active
//   - VELOX_ADMIN_TOKEN env var is unset
//   - SMOKE_DRIVE_FOLDER_ID env var is unset
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverD3RequiredOptional(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover D3 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover D3 needs a real Drive folder_id")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_d3_required_optional_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_d3_required_optional_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_d3_required_optional_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: D3a (1 child FAILS quickly at TTS) + D3b (1 child
	// SUCCEEDS + 1 FAILS in parallel, ~5-10s for both) + 2x aggregator
	// ticks (30s each). 360s default leaves comfortable headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=360")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_d3_required_optional_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover D3 smoke failed: %v", err)
	}
}
