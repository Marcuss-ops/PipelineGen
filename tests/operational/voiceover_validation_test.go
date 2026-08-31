// Package operational holds thin Go test wrappers around the bash smoke
// scripts in this directory. The actual HTTP probes live in
// voiceover_validation_smoke.sh and reuse lib/common.sh (the canonical
// smoke-test harness for the project).
//
// This file uses ONLY the Go standard library — no internal/* imports —
// so it compiles cleanly even when the rest of the module carries the
// six pre-existing build issues tracked under
// architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04
// (monitor/enqueue.go, monitor/scheduler.go, stockpipeline/run_upload.go,
// app/module_media.go, images/routing, workerruntime/{preflight,run}.go).
//
// The wrapper exists so the smoke is reachable from `go test` (CI
// integration, code editors, go test -run patterns) without losing
// the canonical bash harness for live operator use.
package operational

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestVoiceoverValidationFailClosed is the FASE A gate for the voiceover
// pipeline: 3 fail-closed validation cases per the Action Plan 2026-07-04.
//
// Skipped when:
//   - go test -short is active (no live HTTP probes in short mode)
//   - VELOX_ADMIN_TOKEN env var is unset (no auth, no live server)
//
// When executed, the wrapper shells out to the bash smoke and propagates
// the exit code. The bash script's stdout/stderr is captured and logged
// via t.Logf so CI output shows the full failure mode.
func TestVoiceoverValidationFailClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live HTTP probes in -short mode")
	}
	if os.Getenv("VELOX_ADMIN_TOKEN") == "" {
		t.Skip("VELOX_ADMIN_TOKEN not set; skipping live voiceover validation test")
	}
	if os.Getenv("SMOKE_DB") == "" {
		t.Skip("SMOKE_DB not set; live voiceover validation requires an explicit database path")
	}

	// Resolve the bash script relative to this test file's directory.
	// runtime.Caller(0) returns the call-site of runtime.Caller itself,
	// which is inside this test function — its file path is this _test.go.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot resolve voiceover_validation_smoke.sh path")
	}
	scriptPath := filepath.Join(filepath.Dir(thisFile), "voiceover_validation_smoke.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("voiceover_validation_smoke.sh not found at %s: %v", scriptPath, err)
	}

	// Environment variables consumed by the bash script (propagated via
	// os.Environ()):
	//   VELOX_ADMIN_TOKEN (required)   bearer token for HTTP probes
	//   API_BASE          (optional)  host:port of the PipelineGen server
	//                                 (default 127.0.0.1:${VELOX_PORT:-8080})
	//   SMOKE_DB          (optional)  path to media.db.sqlite
	//                                 (default data/media/media.db.sqlite)
	cmd := exec.Command("bash", scriptPath)
	// Cap the wall clock for the wrapper invocation; the bash script
	// itself enforces its own SMOKE_TIMEOUT_SECONDS default of 180.
	cmd.Env = append(os.Environ(), "SMOKE_TIMEOUT_SECONDS=60")
	out, err := cmd.CombinedOutput()
	t.Logf("voiceover_validation_smoke.sh output:\n%s", out)
	if err != nil {
		// Exit 2 = setup error (missing token, missing binary, missing SMOKE_DB);
		// exit 124 = wall-clock timeout. Both are honest failure signals
		// and should fail the test loud per godlike/07 no-fake-availability.
		t.Fatalf("voiceover validation smoke failed: %v", err)
	}
}
