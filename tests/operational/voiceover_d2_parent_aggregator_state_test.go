// Package operational — TestVoiceoverD2ParentAggregatorState is the FASE D2
// gate: 1 required item (it-IT + DiegoNeural) produces a parent whose
// state machine transitions through waiting_children → succeeded. The
// aggregator's finalizeParent (internal/application/voiceover/jobs/parent_aggregator.go)
// calls jobsSvc.FinalizeAggregateParent which writes both the JSON key
// (result_json.parent_state) AND the typed column (parent_state_typed,
// P1.2 migration 129).
//
// 7 assertions: 1 parent + 1 child SUCCEEDED + 3 state assertions
// (initial / final / transition) + 1 typed-column-written (P1.2) +
// 1 broker status.
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

func TestVoiceoverD2ParentAggregatorState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover D2 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover D2 needs a real Drive folder_id")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_d2_parent_aggregator_state_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_d2_parent_aggregator_state_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_d2_parent_aggregator_state_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: 1 child (TTS + Drive) + ParentAggregator tick (30s
	// production). 240s default leaves comfortable headroom for the
	// initial→final state transition + a possible retry tick.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=240")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_d2_parent_aggregator_state_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover D2 smoke failed: %v", err)
	}
}
