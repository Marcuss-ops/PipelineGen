// Package operational — TestVoiceoverC3DriveFolderInvalid is the FASE C3 gate:
// destination.kind=explicit with a fake (33-char 'f' fill) folder_id triggers
// a Drive upload failure in the child job. Atteso: child FAILED at Drive
// upload step, parent FAILED, 0 voiceovers / media_assets / outbox events
// (no finalizer run when Drive upload fails).
//
// Pure-stdlib wrapper (no internal/* imports) so it compiles cleanly even
// with the 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skipped when:
//   - go test -short is active
//   - VELOX_ADMIN_TOKEN env var is unset
//
// Note: C3 does NOT need SMOKE_DRIVE_FOLDER_ID (the test uses its own
// fake folder_id by default). The real SMOKE_DRIVE_FOLDER_ID is only
// required to ensure the fake one does NOT match the real one (safety
// guard); if both are unset and the test would silently degrade, the
// bash script's sanity guard aborts with exit 2.
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverC3DriveFolderInvalid(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover C3 test")
	}
	smokeDB := os.Getenv("SMOKE_DB")
	if smokeDB == "" {
		t.Skip("SMOKE_DB not set; live voiceover C3 requires an explicit database path")
	}
	if _, err := os.Stat(smokeDB); os.IsNotExist(err) {
		t.Skipf("SMOKE_DB %s missing; skipping test", smokeDB)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_c3_drive_folder_invalid_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_c3_drive_folder_invalid_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_c3_drive_folder_invalid_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock: 1 child (fails at Drive upload — fast, ~1-3s) + parent
	// aggregator tick (~5s). 180s default leaves comfortable headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=180")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_c3_drive_folder_invalid_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover C3 smoke failed: %v", err)
	}
}
