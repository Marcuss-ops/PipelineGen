package vidrush

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func stockQueryLaneSegment() scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-stock", SceneID: "scene-stock", Position: 1,
		Text:     "Corrado Augias attraversa la piazza di Garlasco.",
		TextHash: "stock-query-lane-hash",
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{
				Value: "Corrado Augias", Type: "PERSON", CanonicalID: "person:corrado-augias",
				SearchIntents: []string{"Corrado Augias"},
			}},
			ImageEntityCanonicalIDs: map[string]string{"Corrado Augias": "person:corrado-augias"},
			ImageQueries:            []string{"Corrado Augias studio Rai", "Garlasco piazza"},
		},
	}
}

// TestStockBoundContextualQueriesAreNotReclassifiedAsPerson pins the query
// classification contract behind the R22 payload bug: a contextual query that
// merely mentions the person must not be treated as an identity query (which
// would then be rejected by the portrait gate and leave the scene empty), while
// the explicit portrait query must resolve to the canonical PERSON.
func TestStockBoundContextualQueriesAreNotReclassifiedAsPerson(t *testing.T) {
	segment := stockQueryLaneSegment()

	identity, ok, err := personCatalogIdentityForSegmentQuery(segment, "Corrado Augias portrait")
	if err != nil {
		t.Fatalf("identity query: %v", err)
	}
	if !ok || identity.CanonicalEntityID != "person:corrado-augias" {
		t.Fatalf("portrait query classified as (%q, %v), want the canonical PERSON identity", identity.CanonicalEntityID, ok)
	}

	for _, query := range []string{"Corrado Augias studio Rai", "Garlasco piazza"} {
		if _, ok, err := personCatalogIdentityForSegmentQuery(segment, query); err != nil {
			t.Fatalf("%q: %v", query, err)
		} else if ok {
			t.Fatalf("contextual query %q was reclassified as a PERSON identity; the portrait gate would then discard its results", query)
		}
	}
}

// TestStockBoundDiscoveryKeepsSomeLaneWhenContextualQueriesExist is the
// "never a silent zero" invariant: with entity mode on and only contextual
// queries carrying the person's name, discovery must still search (identity
// lane, or the contextual queries when no identity exists) instead of
// returning BYPASS.
func TestStockBoundDiscoveryKeepsSomeLaneWhenContextualQueriesExist(t *testing.T) {
	segment := stockQueryLaneSegment()

	identityLane := planInternetImageDiscovery(
		segment.Insights.ImageQueries,
		[]string{"Corrado Augias portrait"},
		nil, true, false, nil,
	)
	if identityLane.Mode != internetImageDiscoverySearch {
		t.Fatalf("discovery mode = %v, want a real search", identityLane.Mode)
	}
	if len(identityLane.Queries) != 1 || identityLane.Queries[0] != "Corrado Augias portrait" {
		t.Fatalf("discovery queries = %v, want the identity portrait lane", identityLane.Queries)
	}

	// No extracted identity yet (VisualNER has not run): the contextual lane is
	// the only retrieval intent and must survive instead of being discarded.
	contextualOnly := planInternetImageDiscovery(
		segment.Insights.ImageQueries,
		nil,
		nil, true, false, nil,
	)
	if contextualOnly.Mode != internetImageDiscoverySearch {
		t.Fatalf("contextual-only discovery mode = %v, want a real search (no silent BYPASS)", contextualOnly.Mode)
	}
	if len(contextualOnly.Queries) == 0 {
		t.Fatal("contextual-only discovery produced zero queries")
	}

	// No lane at all is the only legitimate BYPASS.
	if empty := planInternetImageDiscovery(nil, nil, nil, true, false, nil); empty.Mode != internetImageDiscoveryBypass {
		t.Fatalf("empty discovery mode = %v, want BYPASS", empty.Mode)
	}
}

// TestStockBoundClearingKeepsEntityCandidatesAsOverlays pins the stock
// isolation contract: provider scene media is cleared, entity candidates stay
// available for annotation binding, and the sidecar stays enabled for the
// stock-bound entity-image policy.
func TestStockBoundClearingKeepsEntityCandidatesAsOverlays(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled},
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"}, Scope: "per_scene",
			}},
		},
	}
	if !stockBoundEntityImageMaterializationEnabled(plan) {
		t.Fatal("stock-bound entity-image sidecar must stay enabled for per-scene PERSON overlays")
	}

	segment := scriptpkg.VidRushSegmentResult{
		SegmentID: "scene-stock",
		Assets: scriptpkg.SegmentAssetSelection{
			Candidates:      []scriptpkg.SegmentAssetCandidate{{AssetID: "entity-image-11", Provider: scriptpkg.VidRushProviderInternetImages}},
			SecondaryImages: []scriptpkg.SegmentAssetCandidate{{AssetID: "contextual-1", Provider: scriptpkg.VidRushProviderInternetImages}},
			GeneratedImages: []scriptpkg.SegmentAssetCandidate{{AssetID: "generated-1"}},
		},
	}
	clearStockBoundSceneImages(&segment)
	if len(segment.Assets.SecondaryImages) != 0 || len(segment.Assets.GeneratedImages) != 0 {
		t.Fatalf("stock clearing left provider scene media: secondary=%d generated=%d",
			len(segment.Assets.SecondaryImages), len(segment.Assets.GeneratedImages))
	}
	if len(segment.Assets.Candidates) != 1 || segment.Assets.Candidates[0].AssetID != "entity-image-11" {
		t.Fatalf("stock clearing dropped the entity overlay candidates: %+v", segment.Assets.Candidates)
	}
}
