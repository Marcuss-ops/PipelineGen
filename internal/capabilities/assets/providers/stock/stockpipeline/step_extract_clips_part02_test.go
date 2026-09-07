package stockpipeline

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStockExtractClips_OutOfRange_FailsClosed is the Front 5
// regression guard. It asserts the pre-cut duration validation
// contract per user spec literal "fallire subito con errore
// leggibile":
//
//  1. StagedAsset.DurationSec is the canonical SSOT for the
//     source duration (60s in this test).
//  2. 1 plan with EndSec=999 (way out of range).
//  3. step.Run returns an error wrapping ErrStockClipsOutOfRange
//     (errors.Is must succeed).
//  4. The cutter is NOT invoked (probe-before-cut contract:
//     the step aborts before reaching cutter.Cut).
//  5. The writer is NOT invoked (no asset write on step failure).
//
// This closes the silent-success class where a clip with EndSec
// > source duration was being cut by ffmpeg (which silently
// truncates or produces a half-broken artifact) and shipped to
// Drive. Per user spec, no auto-clamp; the operator must fix the
// input.
func TestStockExtractClips_OutOfRange_FailsClosed(t *testing.T) {
	// Arrange: source + output paths (real files so the test
	// never reaches the SHA256-compute step — the out-of-range
	// check fires BEFORE the cutter.Cut call).
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// Arrange: 1 plan with EndSec=999 (way out of range).
	plan := ClipPlan{
		SourceID:        "yt-bad-timestamp",
		OutputLogicalID: "planner:oor:0",
		StartSec:        0,
		EndSec:          999, // >>>> source duration 60
		PolicyVersion:   "test-policy-v1",
	}

	// Arrange: writer + cutter (both must NOT be invoked).
	writer := &recordingWriter{}
	cutter := &recordingCutter{}

	state := &RunState{
		Plan: []ClipPlan{plan},
		StagedAssets: []*assets.StagedAsset{
			{
				SourceID:    plan.SourceID,
				LocalPath:   sourcePath,
				DurationSec: 60, // canonical SSOT for the test
			},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{{
				URL:      "https://www.youtube.com/watch?v=bad-timestamp",
				StartSec: 0,
				EndSec:   999,
			}},
			ClipDuration: 19,
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "test-policy-v1",
		},
		state: state,
	}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: base,
		writer:         writer,
		cutter:         cutter,
	}

	// Act.
	step := StockExtractClipsStep{}
	err := step.Run(context.Background(), runner)

	// Assert 1: error is non-nil.
	if err == nil {
		t.Fatal("step.Run returned nil error — expected ErrStockClipsOutOfRange, got nil")
	}

	// Assert 2: errors.Is(err, ErrStockClipsOutOfRange) (godlike/07
	// typed-error contract: callers probe via errors.Is, not by
	// parsing the human-readable prefix).
	if !errors.Is(err, ErrStockClipsOutOfRange) {
		t.Errorf("err = %v, want errors.Is(err, ErrStockClipsOutOfRange) == true", err)
	}

	// Assert 2b: error message carries the operator-readable
	// diagnostic context per user spec "errore leggibile"
	// (EndSec=999.00, source duration=60.00, overrun=939.00s,
	// clip index 0, artifact id). Without these, a future
	// refactor that returns the bare sentinel loses the
	// operator-readable context the user spec explicitly demands.
	for _, sub := range []string{
		"clip[0]",
		"EndSec=999.00",
		"duration=60.00",
		"overrun=939.00s",
		"planner:oor:0",
	} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("err.Error() missing %q\ngot: %v", sub, err)
		}
	}

	// Assert 3: cutter NOT invoked (probe-before-cut contract).
	if cutter.calls != 0 {
		t.Errorf("cutter.calls = %d, want 0 (probe-before-cut contract: step must abort before cutter.Cut)", cutter.calls)
	}

	// Assert 4: writer NOT invoked (no asset write on step failure).
	if writer.calls != 0 {
		t.Errorf("writer.calls = %d, want 0 (no asset write when step fails pre-cut)", writer.calls)
	}
}
