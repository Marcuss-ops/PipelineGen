package vidrush

import (
	"context"
	"errors"
	"fmt"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
)

// augiasReplayPlan is the R23 payload shape reduced to the media contract: five
// stock-bound scenes, per-scene PERSON entity images, and the canonical
// portrait query. The provider lanes are dead in this test, so every image must
// come from the durable catalog.
func augiasReplayPlan() *scriptpkg.ResolvedGenerationPlan {
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title: "augias replay", Language: "it", ImagesPerScene: 1,
		MediaPlan: mediadomain.MediaPlanSpec{
			ProviderPolicy: mediadomain.MediaProviderPolicy{
				InternetImages:  mediadomain.MediaToggleEnabled,
				YouTube:         mediadomain.MediaToggleDisabled,
				ImageGeneration: mediadomain.MediaToggleDisabled,
			},
			Extraction: mediadomain.MediaExtractionPolicy{EntityImages: mediadomain.EntityImagePolicy{
				Enabled: true, EntityTypes: []string{"PERSON"}, Scope: "per_scene",
			}},
		},
	}
	for i := 1; i <= 5; i++ {
		segmentID := fmt.Sprintf("scene-%02d", i)
		plan.Segments = append(plan.Segments, scriptpkg.ScriptSegment{ID: segmentID, StockFolderID: "stock-folder"})
		plan.StockBindings = append(plan.StockBindings, scriptpkg.StockBindingInput{
			Index: i, SceneID: segmentID, SegmentID: segmentID, Source: "drive",
		})
	}
	return plan
}

func augiasReplaySegment(id string) scriptpkg.VidRushSegmentResult {
	return scriptpkg.VidRushSegmentResult{
		SegmentID: id, SceneID: id, Position: 0,
		Text:     "Corrado Augias racconta la storia.",
		TextHash: "augias-replay-" + id,
		Insights: scriptpkg.SegmentInsights{
			Entities: []scriptpkg.ExtractedEntity{{
				Value: "Corrado Augias", Type: "PERSON", CanonicalID: "person:corrado-augias",
				SearchIntents: []string{"Corrado Augias"},
			}},
			ImageEntityCanonicalIDs: map[string]string{"Corrado Augias": "person:corrado-augias"},
			ImageQueries:            []string{"Corrado Augias portrait"},
		},
	}
}

// TestAugiasFiveSceneReplayMaterializesOneImagePerScene is the offline replay
// of the failing R23 shape: five stock-bound scenes, zero provider
// availability, and a warm verified catalog. Every scene must leave discovery
// with candidates and materialization must publish them from the catalog
// without a single Acquire/Finalize (no network, no Ollama).
func TestAugiasFiveSceneReplayMaterializesOneImagePerScene(t *testing.T) {
	resetEntityImageCatalogCaches()
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)

	repo := newIntegrationEntityImageCatalog()
	// More verified portraits than scenes: the same warm catalog serves the
	// whole replay.
	seedVerifiedPortraitCatalog(t, repo, "Corrado Augias", 8)

	plan := augiasReplayPlan()
	deadProvider := &refreshImageSearcher{err: errors.New("searxng: context deadline exceeded")}
	fanout := NewVidRushProviderFanoutWithCatalog(deadProvider, nil, repo)

	segments := make([]scriptpkg.VidRushSegmentResult, 0, 5)
	for _, binding := range plan.StockBindings {
		resolved, err := fanout.ResolveProviders(context.Background(), plan, augiasReplaySegment(binding.SegmentID))
		if err != nil {
			t.Fatalf("%s: ResolveProviders: %v", binding.SegmentID, err)
		}
		if len(resolved.Assets.Candidates) == 0 {
			t.Fatalf("%s: no candidate reached materialization (warnings=%v)", binding.SegmentID, resolved.Warnings)
		}
		segments = append(segments, resolved)
	}

	provider := &catalogReuseImageProvider{}
	finalizer := &catalogReuseFinalizer{}
	registry := NewVidRushAssetProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	processor := NewVidRushMaterializationProcessorWithCatalog(registry, finalizer, nil, repo)

	specScene := scriptpkg.SpecSceneOutput{Version: 1}
	for i, binding := range plan.StockBindings {
		specScene.Scenes = append(specScene.Scenes, scriptpkg.SpecScene{
			ID: binding.SegmentID, SegmentID: binding.SegmentID, Index: i,
			Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				CanonicalName: "Corrado Augias", Text: "Corrado Augias", Type: "PERSON",
				CanonicalEntityID: "person:corrado-augias",
			}}},
		})
	}
	result, err := processor.Process(context.Background(), plan, ProcessInput{SpecScene: specScene, VidRushSegments: segments})
	if err != nil {
		t.Fatalf("materialization: %v", err)
	}
	if len(result.VidRushSegments) != 5 {
		t.Fatalf("materialized segments = %d, want 5", len(result.VidRushSegments))
	}
	if len(result.UpdatedSpecScene.Scenes) != 5 {
		t.Fatalf("projected scenes = %d, want 5", len(result.UpdatedSpecScene.Scenes))
	}

	for _, scene := range result.UpdatedSpecScene.Scenes {
		if scene.Annotations == nil || len(scene.Annotations.PrimaryEntities) == 0 {
			t.Fatalf("%s lost its PERSON annotation", scene.ID)
		}
		image := scene.Annotations.PrimaryEntities[0].Image
		if image == nil || image.Status != "resolved" || image.AssetID == "" || image.DriveLink == "" {
			t.Fatalf("%s entity image binding = %+v, want a resolved Drive-backed portrait", scene.ID, image)
		}
	}
	for _, scene := range result.VidRushSegments {
		// Stock isolation: the verified portraits are overlays, never scene
		// media, and they do not cross the certification boundary as
		// provider candidates.
		if len(scene.Assets.SecondaryImages) != 0 || len(scene.Assets.GeneratedImages) != 0 || len(scene.Assets.Candidates) != 0 {
			t.Fatalf("%s leaked provider media on a stock-bound scene: candidates=%d secondary=%d generated=%d",
				scene.SegmentID, len(scene.Assets.Candidates), len(scene.Assets.SecondaryImages), len(scene.Assets.GeneratedImages))
		}
	}
	if calls := provider.acquireCalls.Load(); calls != 0 {
		t.Fatalf("image downloads = %d, want 0: catalog materializations must be reused", calls)
	}
	if calls := finalizer.finalizeCalls.Load(); calls != 0 {
		t.Fatalf("Drive uploads/finalizations = %d, want 0", calls)
	}
}
