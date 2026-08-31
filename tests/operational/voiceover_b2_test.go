// Package operational — TestVoiceoverMultiItemMixedText is the FASE B2 gate
// for the voiceover pipeline: 3 items (it-IT + en-US + pt-BR) produce
// 1 parent + 3 child + 3 voiceovers + 3 media_assets + 3 outbox events.
//
// Pure-stdlib wrapper (no internal/* imports) so it compiles cleanly even
// with the 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skipped when:
//   - go test -short is active (no live HTTP probes in short mode)
//   - VELOX_ADMIN_TOKEN env var is unset (no auth, no live server)
//   - SMOKE_DRIVE_FOLDER_ID env var is unset (the bash script aborts with
//     exit 2 in that case, which we surface via t.Skipf for clearer CI
//     messaging — re-enable by setting the env var)
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverMultiItemMixedText(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover B2 test")
	}
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover B2 needs a real Drive folder_id for destination.kind=explicit")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_b2_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_b2_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_b2_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock budget: 3 children in parallel + TTS + Drive upload +
	// ParentAggregator tick (~5s). 180s default leaves comfortable headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=180")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_b2_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover B2 smoke failed: %v", err)
	}
}
