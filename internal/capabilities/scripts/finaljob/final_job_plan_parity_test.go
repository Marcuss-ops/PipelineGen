package finaljob

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// TestFinalJobPlanSingleSerializationMatchesStringChain pins DEMOL-08: the
// in-memory hand-off path (build -> seal -> one marshal) must emit the exact
// same bytes and digest as the string entry point that still decodes and
// re-encodes the plan. If a future edit makes a step serialize on its own, the
// two paths drift and the Master would see a different idempotency key.
func TestFinalJobPlanSingleSerializationMatchesStringChain(t *testing.T) {
	videoSHA := strings.Repeat("a", 64)
	audioSHA := strings.Repeat("b", 64)
	scenes := []FinalJobRemoteScene{{
		SceneID:         "scene-0000",
		DurationSeconds: 5,
		Kind:            "stock",
		Stock: map[string]any{
			"asset_id": "base-video", "sha256": videoSHA, "size_bytes": int64(1000),
			"duration_ms": int64(5000), "start_ms": int64(0),
		},
	}}
	audio := contract.FinalAudioReference{
		DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true,
		PlanSHA256: strings.Repeat("d", 64),
	}
	audioRef := map[string]any{
		"asset_id": "final-audio", "sha256": audioSHA, "size_bytes": int64(200),
		"duration_ms": int64(5000),
	}

	wantJSON, wantSHA, err := FinalJobCompiledPlanPayloadWithStockPool(scenes, nil, nil, audio, audioRef, 5_000_000, "", nil)
	if err != nil {
		t.Fatalf("string chain: %v", err)
	}

	plan, stockAssetIDs, err := buildFinalJobCompiledPlan(scenes, nil, nil, audio, audioRef, 5_000_000, "", nil)
	if err != nil {
		t.Fatalf("in-memory build: %v", err)
	}
	if err := finalJobSealCompiledPlan(plan, scenes, stockAssetIDs); err != nil {
		t.Fatalf("in-memory seal: %v", err)
	}
	gotJSON, gotSHA, err := marshalFinalJobCompiledPlan(plan)
	if err != nil {
		t.Fatalf("in-memory marshal: %v", err)
	}
	if gotJSON != wantJSON {
		t.Fatalf("compiled plan bytes diverge between the in-memory path and the string chain")
	}
	if gotSHA != wantSHA {
		t.Fatalf("compiled plan digest = %s, want %s", gotSHA, wantSHA)
	}
}
