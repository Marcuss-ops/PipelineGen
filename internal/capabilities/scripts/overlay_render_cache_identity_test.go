package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
	"testing"
)

// cacheIdentityPlan builds the minimal plan whose render context digest is
// exercised below: one entity-style item, the only class whose pixels depend
// on the worker's sampling identity.
func cacheIdentityPlan(planID, projectID string) capmodel.OverlayPlan {
	return capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       "video-1",
		ProjectID:     projectID,
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		Items: []capmodel.OverlayItem{
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
	plan.Background = &capmodel.OverlayBackground{
		Kind: "image", AssetRefs: []capmodel.OverlayAssetRef{{AssetID: "bg", URL: "https://example.com/a.png"}},
	}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("a URL-only background must not name a reusable cache row")
	}
	plan.Background.AssetRefs[0].SHA256 = strings.Repeat("a", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("a content-addressed background must be cacheable")
	}
	plan.Source = &capmodel.OverlaySource{AssetID: "source", SHA256: "not-a-hash", Path: "source.mp4"}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("an invalid source digest must not name a reusable cache row")
	}
	plan.Source.SHA256 = strings.Repeat("b", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("content-addressed source and background must be cacheable")
	}
	plan.Items[0].AssetRefs = []capmodel.OverlayAssetRef{{AssetID: "item", URL: "https://example.com/item.png"}}
	if got := overlayPlanRenderContextDigest(plan); got != "" {
		t.Fatal("a URL-only item asset must not name a reusable cache row")
	}
	plan.Items[0].AssetRefs[0].SHA256 = strings.Repeat("c", 64)
	if got := overlayPlanRenderContextDigest(plan); got == "" {
		t.Fatal("a content-addressed item asset must be cacheable")
	}
}

func TestOverlayRenderCacheIdentityPreservesBackgroundAssetOrder(t *testing.T) {
	first := cacheIdentityPlan("run-1", "project-1")
	first.Background = &capmodel.OverlayBackground{
		Kind: "image",
		AssetRefs: []capmodel.OverlayAssetRef{
			{AssetID: "first", SHA256: strings.Repeat("a", 64)},
			{AssetID: "second", SHA256: strings.Repeat("b", 64)},
		},
	}
	second := cacheIdentityPlan("run-1", "project-1")
	second.Background = &capmodel.OverlayBackground{
		Kind: "image",
		AssetRefs: []capmodel.OverlayAssetRef{
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
