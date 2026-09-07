package stockpipeline

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"os"
	"path/filepath"
	"testing"
)

// TestStockExtractClips_ThirtyClips_SingleCutRequest asserts the
// batch-cutting contract at scale: 30 clip plans on the same source
// must be folded into exactly one CutRequest carrying 30 CutJobs.
func TestStockExtractClips_ThirtyClips_SingleCutRequest(t *testing.T) {
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	plans := make([]ClipPlan, 30)
	clips := make([]ClipSpec, 30)
	for i := 0; i < 30; i++ {
		plans[i] = ClipPlan{
			SourceID:        "yt-thirty-clips",
			OutputLogicalID: fmt.Sprintf("planner:thirty:%d", i),
			StartSec:        float64(i * 5),
			EndSec:          float64(i*5 + 5),
			PolicyVersion:   "test-policy-v1",
		}
		clips[i] = ClipSpec{
			URL:      "https://www.youtube.com/watch?v=thirty-clips",
			StartSec: float64(i * 5),
			EndSec:   float64(i*5 + 5),
		}
	}

	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &RunState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: "yt-thirty-clips", LocalPath: sourcePath, DurationSec: 200},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips:        clips,
			ClipDuration: 5,
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

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert: exactly one CutRequest was issued for the 30-clip source group.
	if len(cutter.requests) != 1 {
		t.Fatalf("expected 1 CutRequest for 30-clip source group, got %d", len(cutter.requests))
	}

	req := cutter.requests[0]
	if len(req.Jobs) != 30 {
		t.Fatalf("CutRequest.Jobs length = %d, want 30", len(req.Jobs))
	}

	// Assert: writer was called once per produced clip.
	if writer.calls != 30 {
		t.Errorf("writer.calls = %d, want 30", writer.calls)
	}
}
