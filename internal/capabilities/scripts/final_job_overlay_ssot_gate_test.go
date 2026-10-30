package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	overlaymodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The 2026-09-30 "one shot and done" hardening, re-pinned on the
// pure-manifest wire (OVERLAY-MANIFEST-CONTRACT.md §4): the finalize payload
// is the LAST gate before the remote Master and must fail closed at build
// time on every wire fact the worker cannot recover from — one overlay per
// rendered content identity (the duplicate-image incident class), and a
// dedicated replace row that never covers composition frames.
//
// What MOVED with the cutover, on purpose: overlapping placements are now
// ADMITTED on the manifest wire. Every entry reads its frames from ONE
// certified artifact, so overlapping replacement is idempotent and the baked
// z-order (track) is authoritative — the old non-intersection rule existed
// only because each entry carried its own clip under mode=replace. The split
// chart gate is gone for the same reason: a chart group arrives pre-composed
// inside the single timeline artifact, which is exactly what the old gate
// demanded.

// ssotGateResult builds a minimal certified run whose finalize payload places
// the given overlay items through ONE timeline artifact plus its manifest.
func ssotGateResult(items []capabilityoverlay.OverlayItem, artifact *RenderArtifact) *GenerateResult {
	return &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 5_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 5_000_000}}},
		FinalAudio:        certifiedFinalAudio(5000),
		Scenes:            []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "run-1",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: items,
		},
		OverlayRender: manifestOverlayRender(items, artifact),
	}
}

func ssotTimelineArtifact(sha string) *RenderArtifact {
	return &RenderArtifact{ID: "timeline-artifact", DriveFileID: "timeline-drive", SHA256: sha, SizeBytes: 50, FrameCount: 120, CopyEligible: true}
}

func TestFinalJobManifestPreservesPackedOffsetsAndRejectsPlacement(t *testing.T) {
	item := capabilityoverlay.OverlayItem{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", StartUS: 2_000_000, DurationUS: 1_000_000}
	result := ssotGateResult([]capabilityoverlay.OverlayItem{item}, ssotTimelineArtifact(strings.Repeat("d", 64)))
	in, out := int64(0), int64(1_000_000)
	result.OverlayRender.Manifest.Entries[0].ArtifactInUS = &in
	result.OverlayRender.Manifest.Entries[0].ArtifactOutUS = &out
	_, manifest, err := finalJobOverlayHandoff(context.Background(), result, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest["entries"].([]any)[0].(map[string]any)
	if entry["artifact_in_us"] != in || entry["artifact_out_us"] != out {
		t.Fatalf("packed offsets lost at final handoff: %#v", entry)
	}
	result.OverlayRender.Manifest.Entries[0].Placement = &overlaymodel.ManifestPlacement{WPx: 10, HPx: 10}
	if _, _, err := finalJobOverlayHandoff(context.Background(), result, nil); err == nil {
		t.Fatal("unsupported spatial placement must fail closed")
	}
}

// TestFinalJobManifestRejectsDuplicateContentHash pins gate 1 on the manifest
// wire: two voices with the same rendered content identity are the duplicate-
// image incident in miniature — the run must fail closed instead of shipping
// the same visual twice (manifest validation: one content one overlay).
func TestFinalJobManifestRejectsDuplicateContentHash(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 100, EndMs: 2100, Text: "first"},
		{ID: "overlay-b", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 3000, EndMs: 5000, Text: "second"},
	}
	result := ssotGateResult(items, ssotTimelineArtifact(strings.Repeat("d", 64)))
	duplicate := strings.Repeat("c", 64)
	result.OverlayRender.Manifest.Entries[0].ContentHash = duplicate
	result.OverlayRender.Manifest.Entries[1].ContentHash = duplicate
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "one content one overlay") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want duplicate-content fail-closed gate", err)
	}
}

// TestFinalJobHandoffRejectsRowSharingTimelineArtifact pins that a dedicated
// replace row can never reuse the composition's certified bytes: the timeline
// artifact is admitted exactly once on the handoff.
func TestFinalJobHandoffRejectsRowSharingTimelineArtifact(t *testing.T) {
	sha := strings.Repeat("d", 64)
	phrase := capabilityoverlay.OverlayItem{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 100, EndMs: 2100, Text: "first"}
	mapItem := capabilityoverlay.OverlayItem{ID: "map-a", SceneID: "scene-1", Kind: "dynamic_map", StartMs: 3000, EndMs: 5000, Map: &capabilityoverlay.MapOverlay{}}
	result := ssotGateResult([]capabilityoverlay.OverlayItem{phrase}, ssotTimelineArtifact(sha))
	result.OverlayPlan.Items = append(result.OverlayPlan.Items, mapItem)
	result.OverlayRender.Items = append(result.OverlayRender.Items, OverlayItemRenderReference{
		ItemID: "map-a", Status: "SUCCEEDED",
		Artifact: &RenderArtifact{ID: "map-artifact", DriveFileID: "map-drive", SHA256: sha, SizeBytes: 60, CopyEligible: true},
	})
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "same artifact") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want duplicate-artifact fail-closed gate", err)
	}
}

// TestFinalJobManifestAdmitsOverlappingEntriesInShortScene pins the cutover's
// admission boundary: two overlapping 2-second voices in a 3.9-second scene
// are ADMITTED on the manifest wire. The old wire had to reject them (two
// clips cannot share a replace frame); one artifact resolves the overlap in
// its own pixels, so the placement is documented, not scheduled away.
func TestFinalJobManifestAdmitsOverlappingEntriesInShortScene(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 0, EndMs: 2000, Text: "first"},
		{ID: "overlay-b", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 1950, EndMs: 3950, Text: "second"},
	}
	result := ssotGateResult(items, ssotTimelineArtifact(strings.Repeat("d", 64)))
	result.CanonicalTimeline.DurationUS = 3_900_000
	result.CanonicalTimeline.Segments[0].DurationUS = 3_900_000
	result.FinalAudio = certifiedFinalAudio(3_900)
	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	manifest := finalize["overlay_manifest"].(map[string]any)
	if entries := manifest["entries"].([]any); len(entries) != 2 {
		t.Fatalf("manifest entries = %#v, want both overlapping voices admitted", entries)
	}
}

// TestFinalJobManifestAdmitsDistinctEntriesOnTheFrameGrid pins the happy path
// the gates must never break: distinct voices with disjoint windows inside the
// certified output duration are placed verbatim.
func TestFinalJobManifestAdmitsDistinctEntriesOnTheFrameGrid(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 10_000, EndMs: 12_000, Text: "first"},
		{ID: "overlay-b", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: 12_500, EndMs: 14_500, Text: "second"},
	}
	result := ssotGateResult(items, ssotTimelineArtifact(strings.Repeat("d", 64)))
	result.CanonicalTimeline.DurationUS = 15_000_000
	result.CanonicalTimeline.Segments[0].DurationUS = 15_000_000
	result.FinalAudio = certifiedFinalAudio(15_000)
	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	manifest := finalize["overlay_manifest"].(map[string]any)
	entries := manifest["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("manifest entries = %#v, want both distinct voices admitted", entries)
	}
	first := entries[0].(map[string]any)
	second := entries[1].(map[string]any)
	if first["in_us"] != int64(10_000_000) || first["out_us"] != int64(12_000_000) ||
		second["in_us"] != int64(12_500_000) || second["out_us"] != int64(14_500_000) {
		t.Fatalf("entry windows = %v-%v / %v-%v, want the rendered placements preserved",
			first["in_us"], first["out_us"], second["in_us"], second["out_us"])
	}
}

// TestFinalJobManifestKeepsEntityCardOverDateCard pins that an entity card
// sharing a window with a date card keeps its RENDERED placement: the old
// wire had to move the card after the date card to respect mode=replace
// non-intersection; the manifest documents both voices with their baked
// z-order instead.
func TestFinalJobManifestKeepsEntityCardOverDateCard(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "date-13-august", SceneID: "scene-1", Kind: "number", StartMs: 275, EndMs: 5275, Text: "13 August 2007"},
		{ID: "entity-chiara", SceneID: "scene-1", Kind: "entity_card", StartMs: 1725, EndMs: 3050, TemplateID: "person_default"},
	}
	result := ssotGateResult(items, ssotTimelineArtifact(strings.Repeat("d", 64)))
	result.CanonicalTimeline.DurationUS = 10_000_000
	result.CanonicalTimeline.Segments[0].DurationUS = 10_000_000
	result.FinalAudio = certifiedFinalAudio(10_000)

	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	manifest := finalize["overlay_manifest"].(map[string]any)
	entries := manifest["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("manifest entries = %#v, want date and entity card", entries)
	}
	byID := make(map[string]map[string]any, len(entries))
	for _, raw := range entries {
		entry := raw.(map[string]any)
		id, _ := entry["item_id"].(string)
		byID[id] = entry
	}
	date, entity := byID["date-13-august"], byID["entity-chiara"]
	if date == nil || entity == nil {
		t.Fatalf("missing manifest entries: date=%v entity=%v", date, entity)
	}
	if date["in_us"] != int64(275_000) || entity["in_us"] != int64(1_725_000) {
		t.Fatalf("moved entries: date=%v entity=%v, want rendered placement preserved", date, entity)
	}
	if entity["track"].(int) <= date["track"].(int) {
		t.Fatalf("entity track %v must draw above date track %v", entity["track"], date["track"])
	}
}

// TestFinalJobChartGroupArrivesPreComposed pins the chart atomicity contract
// on the new wire: the label card and its bars are voices of ONE certified
// timeline artifact — exactly the pre-composition the old split-chart gate
// demanded — so the group can never reach the worker as separate replace
// clips and no gate is needed.
func TestFinalJobChartGroupArrivesPreComposed(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "chart-card", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
			StartMs: 500, EndMs: 4000, Text: "42%\n18%",
			Params: map[string]any{"chart_card": true, "chart_group_id": "chart-scene-1-42-18"}},
		{ID: "chart-bar-0", SceneID: "scene-1", Kind: "shape", TemplateID: "SHAPE",
			StartMs: 500, EndMs: 4000, Params: map[string]any{"chart_bar": true, "chart_group_id": "chart-scene-1-42-18"}},
		{ID: "chart-bar-1", SceneID: "scene-1", Kind: "shape", TemplateID: "SHAPE",
			StartMs: 500, EndMs: 4000, Params: map[string]any{"chart_bar": true, "chart_group_id": "chart-scene-1-42-18"}},
	}
	result := ssotGateResult(items, ssotTimelineArtifact(strings.Repeat("d", 64)))
	result.CanonicalTimeline.DurationUS = 10_000_000
	result.CanonicalTimeline.Segments[0].DurationUS = 10_000_000
	result.FinalAudio = certifiedFinalAudio(10_000)

	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	if overlays := finalize["overlays"].([]any); len(overlays) != 0 {
		t.Fatalf("finalize overlays = %#v, want no per-item replace rows for the chart", overlays)
	}
	manifest := finalize["overlay_manifest"].(map[string]any)
	if entries := manifest["entries"].([]any); len(entries) != 3 {
		t.Fatalf("manifest entries = %#v, want the whole chart group inside one timeline", entries)
	}
	if asset := manifest["asset"].(map[string]any); asset["drive_file_id"] != "timeline-drive" {
		t.Fatalf("timeline asset = %#v, want ONE certified artifact for the whole chart", asset)
	}
}

// TestFinalJobManifestRejectsEntryStartingAfterBaseEnd pins the fail-closed
// base bound: an entry that starts after the certified output ends can never
// be placed, so the handoff fails before any remote attempt.
func TestFinalJobManifestRejectsEntryStartingAfterBaseEnd(t *testing.T) {
	item := capabilityoverlay.OverlayItem{ID: "late-card", SceneID: "scene-1", Kind: "number", StartMs: 5_000, EndMs: 7_000, Text: "too late"}
	result := ssotGateResult([]capabilityoverlay.OverlayItem{item}, ssotTimelineArtifact(strings.Repeat("d", 64)))
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "starts at frame") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want fail-closed past-base entry", err)
	}
}
