// Package operational — TestVoiceoverC2LegacyFallback is the FASE C2 gate:
// only tts_edge_server.py is renamed to .bak (tts_edge.py remains). The Go
// processor falls back to the legacy spawn-per-call path; child SUCCEEDED.
//
// Pure-stdlib wrapper. Skipped on -short, missing token, missing folder_id.
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverC2LegacyFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover C2 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover C2 needs a real Drive folder_id")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_c2_legacy_fallback_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_c2_legacy_fallback_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_c2_legacy_fallback_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: legacy path is slower (Python startup per call) + 5s
	// aggregator tick. 240s default leaves comfortable headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=240")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_c2_legacy_fallback_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover C2 smoke failed: %v", err)
	}
}
