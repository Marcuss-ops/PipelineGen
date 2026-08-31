// Package operational — TestVoiceoverC1TTSMissing is the FASE C1 gate:
// both tts_edge_server.py AND tts_edge.py are renamed to .bak for the
// duration of the test (the bash script restores them via trap on exit).
// Atteso: child FAILED with tts_failed error, parent FAILED, 0 voiceovers /
// media_assets / outbox events (no finalizer run when TTS is missing).
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

func TestVoiceoverC1TTSMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover C1 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover C1 needs a real Drive folder_id")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_c1_tts_missing_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_c1_tts_missing_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_c1_tts_missing_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: 1 child (will fail at TTS stage) + parent aggregator tick
	// (~5s). The bash script enforces its own SMOKE_TIMEOUT_SECONDS; we
	// bump the cap to 180s for headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=180")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_c1_tts_missing_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover C1 smoke failed: %v", err)
	}
}
