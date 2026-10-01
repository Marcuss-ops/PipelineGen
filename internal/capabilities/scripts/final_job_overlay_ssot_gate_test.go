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
// Master's exact rejection boundary: a second overlay starting at 11.95s
// projects to start frame 287, which is still inside the first overlay's
// frame window [240,288) — the Master replaces whole frames and rejects
// intersecting replace windows. The gate must fail the payload at build time.
func TestFinalJobOverlayAssetsRejectsIntersectingFrameWindows(t *testing.T) {
	items, artifacts := ssotGateItems(10_000, 11_950, strings.Repeat("d", 64), strings.Repeat("e", 64))
	result := ssotGateResult(items, artifacts)
	_, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err == nil || !strings.Contains(err.Error(), "intersects overlay") {
		t.Fatalf("BuildFinalJobPayloads error = %v, want intersecting-frame-window fail-closed gate", err)
	}
}

// TestFinalJobOverlayAssetsAdmitsDistinctArtifactsOnTheFrameGrid pins the
// happy path the gates must never break: distinct artifacts whose frame
// windows only touch at the boundary ([3,33) then [33,63)) pass — a shared
// frame INDEX endpoint is exclusive on the end side, exactly the contract the
// Master compares with.
func TestFinalJobOverlayAssetsAdmitsDistinctArtifactsOnTheFrameGrid(t *testing.T) {
	items, artifacts := ssotGateItems(10_000, 12_500, strings.Repeat("d", 64), strings.Repeat("e", 64))
	result := ssotGateResult(items, artifacts)
	_, finalize, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	overlays, ok := finalize["overlays"].([]any)
	if !ok || len(overlays) != 2 {
		t.Fatalf("finalize overlays = %#v, want both distinct overlays admitted", finalize["overlays"])
	}
}
