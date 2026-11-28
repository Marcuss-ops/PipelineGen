package scriptgeneration

import (
	"context"
	"encoding/json"
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
	if source.PlanSHA256 == "" || source.Prepare.CompiledRenderPlanSHA256 != source.PlanSHA256 {
		t.Fatalf("source plan digest = %q / %#v, want a stable PREPARE digest", source.PlanSHA256, source.Prepare.CompiledRenderPlanSHA256)
	}
	var basePlan finalJobCompiledPlan
	if err := json.Unmarshal([]byte(source.Prepare.CompiledRenderPlanJSON), &basePlan); err != nil {
		t.Fatalf("decode split base plan: %v", err)
	}
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
	if source.Finalize.RuntimePayload.OverlayManifest != nil {
		t.Fatal("source FINALIZE must not carry an overlay manifest")
	}
	if len(source.Finalize.RuntimePayload.Overlays) != 0 {
		t.Fatalf("source FINALIZE overlays = %#v, want none", source.Finalize.RuntimePayload.Overlays)
	}
	for _, asset := range source.Finalize.RuntimeAssets {
		if asset.Role == "overlay" || asset.Role == "overlay_timeline" {
			t.Fatalf("source FINALIZE prefetches overlay asset role %q: %+v", asset.Role, asset)
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
	manifest := finalize.RuntimePayload.OverlayManifest
	if manifest == nil || manifest.SchemaVersion != "pipelinegen.overlay-manifest.v1" {
		t.Fatalf("late FINALIZE overlay_manifest = %#v, want the typed manifest document", finalize.RuntimePayload.OverlayManifest)
	}
	overlayAssets := 0
	for _, asset := range finalize.RuntimeAssets {
		if asset.Role == "overlay_timeline" {
			overlayAssets++
		}
	}
	if overlayAssets != 1 {
		t.Fatalf("late FINALIZE overlay_timeline assets = %d, want 1", overlayAssets)
	}
	if got, want := finalize.IdempotencyKey, source.Prepare.IdempotencyKey+"-finalize"; got != want {
		t.Fatalf("late FINALIZE idempotency_key = %q, want %q", got, want)
	}
	if got := finalize.RuntimeAssets; len(got) <= len(source.Finalize.RuntimeAssets) {
		t.Fatalf("late FINALIZE runtime_assets = %d, want the base set plus the overlay supplement", len(got))
	}
	envelope, err := BuildFinalJobSplitEnvelopeV1("run-split", source, finalize)
	if err != nil {
		t.Fatalf("BuildFinalJobSplitEnvelopeV1: %v", err)
	}
	prepareMap, finalizeMap, err := envelope.MasterPayloads()
	if err != nil {
		t.Fatalf("split envelope MasterPayloads: %v", err)
	}
	if prepareMap["compiled_render_plan_json"] != source.Prepare.CompiledRenderPlanJSON {
		t.Fatal("Master PREPARE changed the sealed split plan bytes")
	}
	wireAssets, ok := finalizeMap["runtime_assets"].([]any)
	if !ok || len(wireAssets) != len(finalize.RuntimeAssets) {
		t.Fatalf("Master FINALIZE runtime_assets = %#v, want %d wire entries", finalizeMap["runtime_assets"], len(finalize.RuntimeAssets))
	}
	wirePayload, ok := finalizeMap["runtime_payload"].(map[string]any)
	if !ok || wirePayload["overlay_manifest"] == nil {
		t.Fatalf("Master FINALIZE runtime_payload = %#v, want overlay manifest", finalizeMap["runtime_payload"])
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
	base := source.Finalize.RuntimeAssets
	late := finalize.RuntimeAssets
	seen := make(map[string]bool, len(late))
	for _, asset := range late {
		id := asset.AssetID
		if seen[id] {
			t.Fatalf("late FINALIZE duplicated runtime asset %q", id)
		}
		seen[id] = true
	}
	for _, asset := range base {
		if !seen[asset.AssetID] {
			t.Fatalf("late FINALIZE dropped base runtime asset %q", asset.AssetID)
		}
	}
}
