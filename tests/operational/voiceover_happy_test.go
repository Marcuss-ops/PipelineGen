// Package operational — TestVoiceoverHappyPathSingleItem is the FASE B1 gate
// for the voiceover pipeline: 1 item (it-IT + DiegoNeural) produces
// 1 parent + 1 child + 1 voiceover + 1 media_asset + 1 outbox event, all
// SUCCEEDED, with the Drive file present (drive_file_id + drive_link
// non-empty on both voiceovers and media_assets rows).
//
// FASE B1 (per the Action Plan 2026-07-04): this Go wrapper thin-execs
// the bash smoke at voiceover_b1_smoke.sh (FASE-prefix convention for
// grep-ability; the Go test keeps the user-specified happy name to honor
// the literal request). Operators searching by FASE number should grep
// for `voiceover_b1_` to find both files.
//
// Pure-stdlib wrapper (no internal/* imports) so it compiles cleanly even
// with the 6 pre-existing build issues in
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04.
//
// Skipped when:
//   - go test -short is active (no live HTTP probes in short mode)
//   - VELOX_ADMIN_TOKEN env var is unset (no auth, no live server)
//   - SMOKE_DRIVE_FOLDER_ID env var is unset (the bash script aborts with
//     exit 2 in that case; we surface via t.Skipf for clearer CI messaging)
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVoiceoverHappyPathSingleItem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover B1 test")
	}
	requireExplicitSmokeDB(t)
	requireExplicitSmokeDB(t)
	if os.Getenv("SMOKE_DRIVE_FOLDER_ID") == "" {
		t.Skip("SMOKE_DRIVE_FOLDER_ID not set; voiceover B1 needs a real Drive folder_id for destination.kind=explicit")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_happy_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_b1_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_b1_smoke.sh not found at %s: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", scriptPath)
	// Wall-clock budget: 1 child (TTS + Drive upload) + ParentAggregator
	// tick (~5s). 180s default leaves comfortable headroom.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=180")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_b1_smoke.sh output:\n%s", out)
	if err != nil {
		t.Fatalf("voiceover B1 smoke failed: %v", err)
	}
}
