package finaljob

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func TestBuildEnvelopeDecodesAndValidatesVersionedContract(t *testing.T) {
	plan := CompiledPlanV2{
		PlanVersion: 2,
		DurationUS:  1_000_000,
		Output: PlanOutput{
			Container: "mp4", VideoCodec: "h264", Width: 1920, Height: 1080,
			FPSNum: 24, FPSDen: 1,
		},
		FinalAudio: PlanAudio{AssetID: "audio", SHA256: strings.Repeat("a", 64), SizeBytes: 10, DurationUS: 1_000_000},
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planJSON := string(planBytes)
	prepare := map[string]any{
		"idempotency_key":             "run-1:prepare",
		"job_type":                    "video.render.v2",
		"render_only":                 true,
		"video_name":                  "video",
		"compiled_render_plan_json":   planJSON,
		"compiled_render_plan_sha256": digest.SHA256String(planJSON),
	}
	finalize := map[string]any{
		"idempotency_key": "run-1:finalize",
		"runtime_assets": []any{map[string]any{
			"asset_id": "audio", "sha256": strings.Repeat("a", 64), "size_bytes": int64(10),
		}},
		"runtime_payload": map[string]any{
			"video_name": "video", "script_text": "story", "overlays": json.RawMessage(`[]`),
		},
	}

	envelope, err := BuildEnvelope("run-1", prepare, finalize)
	if err != nil {
		t.Fatalf("BuildEnvelope: %v", err)
	}
	if envelope.SchemaVersion != EnvelopeSchemaV1 || envelope.RunID != "run-1" || envelope.Plan.PlanVersion != 2 {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestBuildEnvelopeRejectsUnknownDTOFields(t *testing.T) {
	_, err := BuildEnvelope("run-1", map[string]any{"unknown": true}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "PREPARE DTO") {
		t.Fatalf("BuildEnvelope error = %v, want strict PREPARE DTO failure", err)
	}
}

func TestBuildEnvelopeRejectsTrailingPlanJSON(t *testing.T) {
	_, err := BuildEnvelope("run-1", map[string]any{
		"compiled_render_plan_json": `{} {}`,
	}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("BuildEnvelope error = %v, want trailing JSON rejection", err)
	}
}
