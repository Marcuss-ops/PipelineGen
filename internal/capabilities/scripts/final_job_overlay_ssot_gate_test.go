package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// The 2026-09-30 "one shot and done" hardening: finalJobOverlayAssets is the
// LAST gate before the remote Master, and the Master's finalize decoder owns
// two wire facts no upstream arm sees end to end — one overlay per rendered
// Drive artifact, and non-intersecting replace frame windows. Every upstream
// dedup (entity arm, context arm, planner/resolver, compose composites) keys
// on its own local identity, so the Milton duplicate-image incident slipped
// between them and burned remote worker attempts. These pins hold the
// fail-closed gate: a payload violating either contract must FAIL HERE, at
// build time, never on the 51 worker.

// ssotGateResult builds a minimal certified run whose finalize payload carries
// the given overlay items. Every item resolves to its own published artifact
// keyed by item ID, with the SHA256 the test chooses per case.
func ssotGateResult(items []capabilityoverlay.OverlayItem, artifacts map[string]RenderArtifact) *GenerateResult {
	renderItems := make([]OverlayItemRenderReference, 0, len(items))
	for _, item := range items {
		artifact := artifacts[item.ID]
		artifactCopy := artifact
		renderItems = append(renderItems, OverlayItemRenderReference{ItemID: item.ID, Artifact: &artifactCopy})
	}
	return &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 5_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 5_000_000}}},
		FinalAudio:        certifiedFinalAudio(5000),
		Scenes:            []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
		OverlayPlan: &capabilityoverlay.OverlayPlan{
			SchemaVersion: capabilityoverlay.SchemaVersionPlan, PlanID: "plan-1", VideoID: "run-1",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			Items: items,
		},
		OverlayRender: &RenderReference{Items: renderItems},
	}
}

func ssotGateItems(firstStartMS, secondStartMS int64, firstSHA, secondSHA string) ([]capabilityoverlay.OverlayItem, map[string]RenderArtifact) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "overlay-a", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: firstStartMS, EndMs: firstStartMS + 2000, Text: "first"},
		{ID: "overlay-b", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "phrase", StartMs: secondStartMS, EndMs: secondStartMS + 2000, Text: "second"},
	}
	artifacts := map[string]RenderArtifact{
		"overlay-a": {ID: "artifact-a", DriveFileID: "overlay-drive-a", SHA256: firstSHA, SizeBytes: 50, CopyEligible: true},
		"overlay-b": {ID: "artifact-b", DriveFileID: "overlay-drive-b", SHA256: secondSHA, SizeBytes: 60, CopyEligible: true},
	}
	return items, artifacts
}

// TestFinalJobOverlayAssetsRejectsDuplicateDriveArtifact pins gate 1: two
// different overlay items whose renders resolved to the SAME Drive artifact
// (same bytes) are the duplicate-image incident in miniature — the run must
// fail closed instead of shipping the same visual twice.
func TestFinalJobOverlayAssetsRejectsDuplicateDriveArtifact(t *testing.T) {
	sha := strings.Repeat("d", 64)
	items, artifacts := ssotGateItems(100, 3000, sha, sha)
	result := ssotGateResult(items, artifacts)
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "same Drive artifact") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want duplicate-artifact fail-closed gate", err)
	}
}

// TestFinalJobOverlayAssetsRejectsIntersectingFrameWindows pins gate 2 on the
// Master's exact rejection boundary: two overlapping 2-second overlays leave
// no free interval in a 3.9-second scene, so the local resolver rejects before
// dispatch.
func TestFinalJobOverlayAssetsRejectsIntersectingFrameWindows(t *testing.T) {
	items, artifacts := ssotGateItems(0, 1_950, strings.Repeat("d", 64), strings.Repeat("e", 64))
	result := ssotGateResult(items, artifacts)
	result.CanonicalTimeline.DurationUS = 3_900_000
	result.CanonicalTimeline.Segments[0].DurationUS = 3_900_000
	result.FinalAudio = certifiedFinalAudio(3_900)
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "cannot fit a non-overlapping") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want local no-space rejection", err)
	}
}

// TestFinalJobOverlayAssetsAdmitsDistinctArtifactsOnTheFrameGrid pins the
// happy path the gates must never break: distinct artifacts with disjoint
// frame windows inside the certified output duration pass.
func TestFinalJobOverlayAssetsAdmitsDistinctArtifactsOnTheFrameGrid(t *testing.T) {
	items, artifacts := ssotGateItems(10_000, 12_500, strings.Repeat("d", 64), strings.Repeat("e", 64))
	result := ssotGateResult(items, artifacts)
	result.CanonicalTimeline.DurationUS = 15_000_000
	result.CanonicalTimeline.Segments[0].DurationUS = 15_000_000
	result.FinalAudio = certifiedFinalAudio(15_000)
	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	overlays, ok := finalize["overlays"].([]any)
	if !ok || len(overlays) != 2 {
		t.Fatalf("finalize overlays = %#v, want both distinct overlays admitted", finalize["overlays"])
	}
}

func TestFinalJobOverlayAssetsMovesEntityCardAfterDateCard(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{
		{ID: "date-13-august", SceneID: "scene-1", Kind: "number", StartMs: 275, EndMs: 5275, Text: "13 August 2007"},
		{ID: "entity-chiara", SceneID: "scene-1", Kind: "entity_card", StartMs: 1725, EndMs: 3050, TemplateID: "person_default"},
	}
	artifacts := map[string]RenderArtifact{
		"date-13-august": {ID: "artifact-date", DriveFileID: "overlay-drive-date", SHA256: strings.Repeat("d", 64), SizeBytes: 50, CopyEligible: true},
		"entity-chiara":  {ID: "artifact-entity", DriveFileID: "overlay-drive-entity", SHA256: strings.Repeat("e", 64), SizeBytes: 60, CopyEligible: true},
	}
	result := ssotGateResult(items, artifacts)
	result.CanonicalTimeline.DurationUS = 10_000_000
	result.CanonicalTimeline.Segments[0].DurationUS = 10_000_000
	result.FinalAudio = certifiedFinalAudio(10_000)

	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "en"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	overlays, ok := finalize["overlays"].([]any)
	if !ok || len(overlays) != 2 {
		t.Fatalf("finalize overlays = %#v, want date and entity card", finalize["overlays"])
	}
	frames := make(map[string][2]int64, len(overlays))
	for _, raw := range overlays {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("overlay = %#v, want object", raw)
		}
		id, _ := item["id"].(string)
		start, _ := item["start_frame"].(int64)
		end, _ := item["end_frame"].(int64)
		frames[id] = [2]int64{start, end}
	}
	date, entity := frames["date-13-august"], frames["entity-chiara"]
	if date[1] == 0 || entity[1] == 0 {
		t.Fatalf("missing overlay frame windows: date=%v entity=%v", date, entity)
	}
	if entity[0] < date[1] {
		t.Fatalf("entity card frames %v still overlap date card frames %v", entity, date)
	}
}
