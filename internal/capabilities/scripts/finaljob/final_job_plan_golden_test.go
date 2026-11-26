package finaljob

// final_job_plan_golden_test.go freezes the canonical compiled-plan wire
// document (DEMOL-08). The Master derives its idempotency key from these bytes,
// so any change to field order, tags or omitempty rules must fail here instead
// of silently producing a second render for the same plan.
//
// The fixture is deliberately minimal and deterministic: one stock scene, one
// certified final-audio reference, no overlays. Regenerating the expected value
// is a deliberate contract change, never a mechanical update.

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

const (
	// Captured from the certified builder before DEMOL-08's in-memory refactor.
	finalJobPlanGoldenSHA256 = "67b51ccf0f4616095cc2c7d80db5cc7c824752db8c40e70c4815c7e65163b8cd"
)

func finalJobGoldenPlanFixture() ([]FinalJobRemoteScene, contract.FinalAudioReference, map[string]any) {
	scenes := []FinalJobRemoteScene{{
		SceneID:         "scene-0000",
		DurationSeconds: 5,
		Kind:            "stock",
		Stock: map[string]any{
			"asset_id": "base-video", "sha256": strings.Repeat("a", 64), "size_bytes": int64(1000),
			"duration_ms": int64(5000), "start_ms": int64(0),
		},
	}}
	audio := contract.FinalAudioReference{
		DurationUS: 5_000_000, DurationMS: 5000, CopyEligible: true, FinalMix: true,
		PlanSHA256: strings.Repeat("d", 64),
	}
	audioRef := map[string]any{
		"asset_id": "final-audio", "sha256": strings.Repeat("b", 64), "size_bytes": int64(200),
		"duration_ms": int64(5000),
	}
	return scenes, audio, audioRef
}

// TestFinalJobCompiledPlanWireGolden pins the exact canonical JSON digest the
// remote Master receives.
func TestFinalJobCompiledPlanWireGolden(t *testing.T) {
	scenes, audio, audioRef := finalJobGoldenPlanFixture()

	plan, stockAssetIDs, err := buildFinalJobCompiledPlan(scenes, nil, nil, audio, audioRef, 5_000_000, "", nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := finalJobSealCompiledPlan(plan, scenes, stockAssetIDs); err != nil {
		t.Fatalf("seal: %v", err)
	}
	_, gotSHA, err := marshalFinalJobCompiledPlan(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if gotSHA != finalJobPlanGoldenSHA256 {
		t.Fatalf("compiled plan golden digest changed: got %s, want %s", gotSHA, finalJobPlanGoldenSHA256)
	}

	// The string entry point must emit the same frozen document, so a caller
	// that still uses it cannot drift from the in-memory hand-off.
	_, chainSHA, err := FinalJobCompiledPlanPayloadWithStockPool(scenes, nil, nil, audio, audioRef, 5_000_000, "", nil)
	if err != nil {
		t.Fatalf("string chain: %v", err)
	}
	if chainSHA != finalJobPlanGoldenSHA256 {
		t.Fatalf("string chain golden digest changed: got %s, want %s", chainSHA, finalJobPlanGoldenSHA256)
	}
}
