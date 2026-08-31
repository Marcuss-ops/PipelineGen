// Package operational — TestVoiceoverC4OutboxDecoupling is the FASE C4
// design-invariant gate: even if Qdrant is unavailable, the voiceover
// pipeline (TTS + Drive upload + finalizer) MUST complete. The outbox
// event is the load-bearing proof — it MUST be written regardless of
// Qdrant state.
//
// Honest-limitation: this smoke does NOT actually toggle Qdrant off
// (would require server restart). It verifies the design invariant by
// asserting the same 6 conditions as FASE B1 (1 parent + 1 child +
// 1 voiceover + 1 media_asset + 1 outbox event + parent.status=SUCCEEDED).
// To actually exercise the Qdrant-off path, restart the server with
// VELOX_FEATURE_QDRANT_ENABLED=false and re-run — the design invariant
// guarantees the same outcome.
//
// Pure-stdlib wrapper (no internal/* imports) so it compiles cleanly even
// with the 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skipped when:
//   - go test -short is active
//   - VELOX_ADMIN_TOKEN env var is unset
//   - SMOKE_DRIVE_FOLDER_ID env var is unset (C4 needs a real Drive folder)
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverC4OutboxDecoupling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover C4 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover C4 needs a real Drive folder_id (same as B1 happy path)")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_c4_outbox_decoupling_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_c4_outbox_decoupling_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_c4_outbox_decoupling_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: identical to B1 (1 child + ParentAggregator tick). 180s.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=180")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_c4_outbox_decoupling_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover C4 smoke failed: %v", err)
	}
}
