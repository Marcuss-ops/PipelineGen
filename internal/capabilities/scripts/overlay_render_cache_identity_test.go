package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// cacheIdentityPlan builds the minimal plan whose render context digest is
// exercised below: one entity-style item, the only class whose pixels depend
// on the worker's sampling identity.
func cacheIdentityPlan(planID, projectID string) capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       "video-1",
		ProjectID:     projectID,
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		Items: []capoverlay.OverlayItem{
			{
				ID:            "image-focus-reveal__chiara-poggi__entity_image__01",
				Kind:          "entity_image",
				EntityStyleID: "premium_random_v1",
			},
		},
	}
}

func TestOverlayRenderCacheRejectsUnidentifiedSourceAndBackground(t *testing.T) {
	plan := cacheIdentityPlan("run-1", "project-1")
	plan.Background = &capoverlay.OverlayBackground{
		Kind: "image", AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "bg", URL: "https://example.com/a.png"}},
	}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("a URL-only background must not name a reusable cache row")
	}
	if _, ok := overlayManifestCompositionCacheKey(plan, manifestKeyTimeline("run-1"), "epoch"); ok {
		t.Fatal("a manifest with an unidentified background must not be cached")
	}
	plan.Background.AssetRefs[0].SHA256 = strings.Repeat("a", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("a content-addressed background must be cacheable")
	}
	plan.Source = &capoverlay.OverlaySource{AssetID: "source", SHA256: "not-a-hash", Path: "source.mp4"}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("an invalid source digest must not name a reusable cache row")
	}
	plan.Source.SHA256 = strings.Repeat("b", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("content-addressed source and background must be cacheable")
	}
	plan.Items[0].AssetRefs = []capoverlay.OverlayAssetRef{{AssetID: "item", URL: "https://example.com/item.png"}}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("a URL-only item asset must not name a reusable cache row")
	}
	plan.Items[0].AssetRefs[0].SHA256 = strings.Repeat("c", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("a content-addressed item asset must be cacheable")
	}
}

func TestManifestWithUnidentifiedBackgroundStillReachesRender(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items = plan.Items[:1]
	plan.Background = &capoverlay.OverlayBackground{
		Kind: "image", AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "bg", URL: "https://example.com/a.png"}},
	}
	e := &QueueRenderEnqueuer{}
	_, err := e.enqueueOverlayManifestTimeline(context.Background(), plan, overlayRenderableCandidates(plan), nil)
	if err == nil || !strings.Contains(err.Error(), "queue render enqueuer is not configured") {
		t.Fatalf("unidentified background should bypass cache and reach render submission, got %v", err)
	}
}

func TestManifestRejectsUnidentifiedItemAsset(t *testing.T) {
	plan := manifestTestPlan()
	plan.Items = plan.Items[:1]
	plan.Items[0].AssetRefs = []capoverlay.OverlayAssetRef{{AssetID: "item", URL: "https://example.com/item.png"}}
	_, err := buildManifestEntryWorks(plan, overlayRenderableCandidates(plan), "epoch")
	if err == nil || !strings.Contains(err.Error(), "asset has no content-addressed identity") {
		t.Fatalf("unidentified item must not produce a certified manifest content hash, got %v", err)
	}
}

func TestOverlayRenderCacheIdentityPreservesBackgroundAssetOrder(t *testing.T) {
	first := cacheIdentityPlan("run-1", "project-1")
	first.Background = &capoverlay.OverlayBackground{
		Kind: "image",
		AssetRefs: []capoverlay.OverlayAssetRef{
			{AssetID: "first", SHA256: strings.Repeat("a", 64)},
			{AssetID: "second", SHA256: strings.Repeat("b", 64)},
		},
	}
	second := cacheIdentityPlan("run-1", "project-1")
	second.Background = &capoverlay.OverlayBackground{
		Kind: "image",
		AssetRefs: []capoverlay.OverlayAssetRef{
			first.Background.AssetRefs[1], first.Background.AssetRefs[0],
		},
	}
	if overlayPlanRenderContextDigest(first) == overlayPlanRenderContextDigest(second) {
		t.Fatal("background asset order changes the rendered image and must change the cache identity")
	}
}

// TestOverlayRenderCacheIdentityIgnoresRunIdentity pins the reuse contract at
// the cache boundary: plan ids are per-run (run_f…), so a context digest that
// mixed them in would name the same pixels differently on every retry and the
// content-addressed overlay render cache could never hit across runs. The
// digest must key on the plan's stable creative identity — the producer
// project — mirroring the worker's entity-style sample base
// (RenderingGen internal/overlay/entity_style.go, entityStyleSampleKey).
func TestOverlayRenderCacheIdentityIgnoresRunIdentity(t *testing.T) {
	first := cacheIdentityPlan("run-111", "project-1")
	retry := cacheIdentityPlan("run-222", "project-1")
	if overlayPlanRenderContextDigest(first) != overlayPlanRenderContextDigest(retry) {
		t.Fatal("the same project must share one render context digest across runs")
	}
	other := cacheIdentityPlan("run-333", "project-2")
	if overlayPlanRenderContextDigest(first) == overlayPlanRenderContextDigest(other) {
		t.Fatal("different projects must not share a render context digest")
	}
	// Without a project the plan id remains the fallback identity, so two runs
	// still name two contexts (historic behaviour, kept fail-closed).
	fallbackA := cacheIdentityPlan("run-111", "")
	fallbackB := cacheIdentityPlan("run-222", "")
	if overlayPlanRenderContextDigest(fallbackA) == overlayPlanRenderContextDigest(fallbackB) {
		t.Fatal("without a project, distinct plan ids must stay distinct contexts")
	}
	// The explicit re-roll knob still splits contexts: the entity variation
	// seed is hashed on its own, so the same project with a different seed
	// must not be served the other seed's pixels.
	seeded := cacheIdentityPlan("run-222", "project-1")
	seeded.EntityVariationSeed = "roll-1"
	if overlayPlanRenderContextDigest(first) == overlayPlanRenderContextDigest(seeded) {
		t.Fatal("an entity variation seed must change the render context digest")
	}
}
