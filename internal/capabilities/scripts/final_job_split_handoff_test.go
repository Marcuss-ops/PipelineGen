package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// splitHandoffResult is a clip scene with a certified localized render and one
// semantic overlay: the smallest fixture where the serial plan and the split
// base plan must differ by exactly the overlay projection.
func splitHandoffResult() *GenerateResult {
	item := capmodel.OverlayItem{
		ID: "split-item", SceneID: "scene-1", Kind: "number",
		TemplateID: "TIMELINE_DATE_CARD", Text: "28 settembre",
		StartUS: 5_000_000, DurationUS: 5_000_000,
	}
	artifact := &RenderArtifact{
		ID: "timeline-artifact", DriveFileID: "timeline-drive",
		SHA256: strings.Repeat("d", 64), SizeBytes: 100,
		DurationUS: 30_000_000, FrameCount: 720, FPSNum: 24, FPSDen: 1,
	}
	return &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-1", Clip: &ClipReference{ID: "yt_source_clip"},
			Text: map[Language]string{"en": "Dolly lands the hotel joke."},
		}},
		LocalizedRenders: []LocalizedRenderResult{{
			SceneID: "scene-1", Language: "en", ClipID: "yt_source_clip", Status: "UPLOADED",
			AssetID: "derived-render-1", DriveFileID: "render-drive-1",
			DriveLink: "https://drive.google.com/file/d/render-drive-1/view",
			SHA256:    strings.Repeat("e", 64), DurationMS: 12_000, RenderSource: "fresh_gpu",
		}},
		OverlayPlan: &capmodel.OverlayPlan{
			SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "plan-split", VideoID: "run-split",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: []capmodel.OverlayItem{item},
		},
		OverlayRender: manifestOverlayRender([]capmodel.OverlayItem{item}, artifact),
		AudioOutput: AudioOutput{
			CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
				DurationUS: 30_000_000,
				Segments: []capabilityaudio.TimelineSegment{{
					ID: "scene-1", DurationUS: 30_000_000,
					Video:        capabilityaudio.VideoSegment{AssetID: "yt_source_clip"},
					AudioIntents: []capabilityaudio.AudioIntent{{Mode: capabilityaudio.AudioClip, ClipAssetID: "yt_source_clip", SourceDurationUS: 12_000_000, UseOriginalAudio: true}},
				}},
			},
			FinalAudio: certifiedFinalAudio(30_000),
		},
	}
}

func splitHandoffRequest() GenerateRequest {
	return GenerateRequest{Title: "Dolly", SourceLanguage: "en"}
}

func TestBuildFinalJobSourceHandoffSealsTheBasePlanWithoutOverlayWindows(t *testing.T) {
	result := splitHandoffResult()
	req := splitHandoffRequest()
	source, err := BuildFinalJobSourceHandoff(context.Background(), "run-split", req, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobSourceHandoff: %v", err)
	}
	if source.PlanSHA256 == "" || source.Prepare["compiled_render_plan_sha256"] != source.PlanSHA256 {
		t.Fatalf("source plan digest = %q / %#v, want a stable PREPARE digest", source.PlanSHA256, source.Prepare["compiled_render_plan_sha256"])
	}
	basePlan := decodeFinalJobPlanForTest(t, source.Prepare)
	for _, segment := range basePlan.VideoTracks[0].Segments {
		if segment.AssetID == "timeline-artifact" {
			t.Fatalf("base plan contains an overlay window: %+v", segment)
		}
	}
	for _, asset := range basePlan.Assets {
		if asset.AssetID == "timeline-artifact" {
			t.Fatalf("base plan contains the overlay timeline asset: %+v", asset)
		}
	}
	basePayload, _ := source.Finalize["runtime_payload"].(map[string]any)
	if basePayload == nil {
		t.Fatal("source FINALIZE has no runtime_payload")
	}
	if _, present := basePayload["overlay_manifest"]; present {
		t.Fatal("source FINALIZE must not carry an overlay manifest")
	}
	if overlays, present := basePayload["overlays"].([]any); present && len(overlays) != 0 {
		t.Fatalf("source FINALIZE overlays = %#v, want none", overlays)
	}
	for _, raw := range source.Finalize["runtime_assets"].([]any) {
		role := finalJobStringValue(raw.(map[string]any)["role"])
		if role == "overlay" || role == "overlay_timeline" {
			t.Fatalf("source FINALIZE prefetches overlay asset role %q: %#v", role, raw)
		}
	}

	// The serial handoff still exists and remains the reference identity. The
	// split base plan must be a different document, because it has no overlay
	// window and no overlay timeline asset.
	serialPrepare, _, err := BuildFinalJobPayloads(context.Background(), "run-split", req, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	if serialPrepare["compiled_render_plan_sha256"] == source.PlanSHA256 {
		t.Fatal("split PREPARE reused the serial full-plan digest; the overlay projection would be double-counted")
	}

	finalize, err := BuildFinalJobOverlayFinalize(context.Background(), "run-split", req, result, source)
	if err != nil {
		t.Fatalf("BuildFinalJobOverlayFinalize: %v", err)
	}
	runtimePayload := finalize["runtime_payload"].(map[string]any)
	manifest, ok := runtimePayload["overlay_manifest"].(map[string]any)
	if !ok || manifest["schema_version"] != "pipelinegen.overlay-manifest.v1" {
		t.Fatalf("late FINALIZE overlay_manifest = %#v, want the typed manifest document", runtimePayload["overlay_manifest"])
	}
	overlayAssets := 0
	for _, raw := range finalize["runtime_assets"].([]any) {
		if finalJobStringValue(raw.(map[string]any)["role"]) == "overlay_timeline" {
			overlayAssets++
		}
	}
	if overlayAssets != 1 {
		t.Fatalf("late FINALIZE overlay_timeline assets = %d, want 1", overlayAssets)
	}
	if got, want := finalize["idempotency_key"], source.Prepare["idempotency_key"].(string)+"-finalize"; got != want {
		t.Fatalf("late FINALIZE idempotency_key = %v, want %q", got, want)
	}
	if got := finalize["runtime_assets"].([]any); len(got) <= len(source.Finalize["runtime_assets"].([]any)) {
		t.Fatalf("late FINALIZE runtime_assets = %d, want the base set plus the overlay supplement", len(got))
	}
}

func TestBuildFinalJobSourceHandoffDoesNotNeedTheOverlayRender(t *testing.T) {
	withRender := splitHandoffResult()
	sourceWithRender, err := BuildFinalJobSourceHandoff(context.Background(), "run-split", splitHandoffRequest(), withRender, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobSourceHandoff(with render): %v", err)
	}
	withoutRender := splitHandoffResult()
	withoutRender.OverlayRender = nil
	sourceWithoutRender, err := BuildFinalJobSourceHandoff(context.Background(), "run-split", splitHandoffRequest(), withoutRender, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobSourceHandoff(without render): %v", err)
	}
	if sourceWithRender.PlanSHA256 != sourceWithoutRender.PlanSHA256 {
		t.Fatalf("base plan digest changed with the overlay render: %s vs %s", sourceWithRender.PlanSHA256, sourceWithoutRender.PlanSHA256)
	}
	if _, err := BuildFinalJobOverlayFinalize(context.Background(), "run-split", splitHandoffRequest(), withoutRender, sourceWithoutRender); err == nil {
		t.Fatal("late FINALIZE accepted a semantic overlay plan without a published overlay render")
	}
}

func TestBuildFinalJobOverlayFinalizeKeepsTheBaseRuntimeAssetSet(t *testing.T) {
	result := splitHandoffResult()
	source, err := BuildFinalJobSourceHandoff(context.Background(), "run-split", splitHandoffRequest(), result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobSourceHandoff: %v", err)
	}
	finalize, err := BuildFinalJobOverlayFinalize(context.Background(), "run-split", splitHandoffRequest(), result, source)
	if err != nil {
		t.Fatalf("BuildFinalJobOverlayFinalize: %v", err)
	}
	base := source.Finalize["runtime_assets"].([]any)
	late := finalize["runtime_assets"].([]any)
	seen := make(map[string]bool, len(late))
	for _, raw := range late {
		asset := raw.(map[string]any)
		id := finalJobStringValue(asset["asset_id"])
		if seen[id] {
			t.Fatalf("late FINALIZE duplicated runtime asset %q", id)
		}
		seen[id] = true
	}
	for _, raw := range base {
		asset := raw.(map[string]any)
		if !seen[finalJobStringValue(asset["asset_id"])] {
			t.Fatalf("late FINALIZE dropped base runtime asset %q", asset["asset_id"])
		}
	}
}
