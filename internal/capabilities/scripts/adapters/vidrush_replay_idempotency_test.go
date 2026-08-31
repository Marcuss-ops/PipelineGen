package adapters

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type replaySemanticProbe struct {
	calls atomic.Int32
	cache *memoryVidRushCache
}

func (p *replaySemanticProbe) Enrich(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	canonical := canonicalSegmentFromScene(scene)
	key := segmentCacheKey("replay-semantic-v1", canonical.TextHash, plan.Language, plan.Model, plan.PromptVersion)
	if payload, hit, err := p.cache.Get(ctx, "replay-semantic", key); err == nil && hit {
		var result scriptpkg.VidRushSegmentResult
		if err := decodeVidRushCache(payload, &result); err == nil {
			return result, nil
		}
	}
	p.calls.Add(1)
	result := scriptpkg.VidRushSegmentResult{
		SegmentID: canonical.ID, SceneID: canonical.SceneID, Position: canonical.Position,
		Text: canonical.Text, TextHash: canonical.TextHash,
		Insights: scriptpkg.SegmentInsights{SegmentID: canonical.ID, TextHash: canonical.TextHash},
	}
	payload, err := encodeVidRushCache(result)
	if err != nil {
		return result, err
	}
	if err := p.cache.Put(ctx, "replay-semantic", key, payload); err != nil {
		return result, err
	}
	return result, nil
}

type replayProviderProbe struct {
	searches atomic.Int32
	cache    *memoryVidRushCache
}

func (p *replayProviderProbe) ResolveProviders(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	key := segmentCacheKey("replay-discovery-v1", segment.SegmentID, segment.TextHash)
	if payload, hit, err := p.cache.Get(ctx, "replay-discovery", key); err == nil && hit {
		var result scriptpkg.VidRushSegmentResult
		if err := decodeVidRushCache(payload, &result); err == nil {
			return result, nil
		}
	}
	p.searches.Add(1)
	segment.Assets.Candidates = []scriptpkg.SegmentAssetCandidate{{AssetID: "replay-asset", Provider: scriptpkg.VidRushProviderYouTube, SourceURL: "https://youtube.example/replay"}}
	payload, err := encodeVidRushCache(segment)
	if err != nil {
		return segment, err
	}
	if err := p.cache.Put(ctx, "replay-discovery", key, payload); err != nil {
		return segment, err
	}
	return segment, nil
}

type replayMaterializerProbe struct {
	downloads atomic.Int32
	uploads   atomic.Int32
	cache     *memoryVidRushCache
}

func (p *replayMaterializerProbe) Materialize(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	key := segmentCacheKey("replay-materialized-v1", segment.SegmentID, segment.TextHash)
	if payload, hit, err := p.cache.Get(ctx, "replay-materialized", key); err == nil && hit {
		var result scriptpkg.VidRushSegmentResult
		if err := decodeVidRushCache(payload, &result); err == nil {
			return result, nil
		}
	}
	p.downloads.Add(1)
	p.uploads.Add(1)
	for i := range segment.Assets.Candidates {
		segment.Assets.Candidates[i].AssetID = "asset-canonical-replay"
		segment.Assets.Candidates[i].DriveLink = "drive://asset-canonical-replay"
		segment.Assets.Candidates[i].PersistenceStatus = scriptpkg.VidRushStatusPersisted
		segment.Assets.Candidates[i].IndexStatus = scriptpkg.VidRushStatusIndexed
	}
	payload, err := encodeVidRushCache(segment)
	if err != nil {
		return segment, err
	}
	if err := p.cache.Put(ctx, "replay-materialized", key, payload); err != nil {
		return segment, err
	}
	return segment, nil
}

func encodeVidRushCache(value any) ([]byte, error)       { return json.Marshal(value) }
func decodeVidRushCache(payload []byte, value any) error { return json.Unmarshal(payload, value) }

func newReplayCoordinator(cache *memoryVidRushCache, semantic *replaySemanticProbe, provider *replayProviderProbe, materializer *replayMaterializerProbe, plan *scriptpkg.ResolvedGenerationPlan) *scriptgeneration.VidRushIncrementalCoordinator {
	coordinator := scriptgeneration.NewVidRushIncrementalCoordinator(semantic, plan, 1)
	coordinator.SetSegmentProviderResolver(provider)
	coordinator.SetSegmentMaterializer(materializer)
	return coordinator
}

func TestVidRushReplayIsIdempotentAcrossSemanticDiscoveryAndPersistence(t *testing.T) {
	cache := newMemoryVidRushCache()
	plan := &scriptpkg.ResolvedGenerationPlan{Language: "en", Model: "small", PromptVersion: "prompt-v1"}
	semantic := &replaySemanticProbe{cache: cache}
	provider := &replayProviderProbe{cache: cache}
	materializer := &replayMaterializerProbe{cache: cache}

	run := func(runID string) {
		coordinator := newReplayCoordinator(cache, semantic, provider, materializer, plan)
		text := "A stable visual segment"
		if err := coordinator.OnSceneCommitted(context.Background(), scriptgeneration.SceneCommitted{
			RunID: runID, SceneID: "scene-1", SceneIndex: 0, Text: text, TextHash: scriptgeneration.SceneTextHash(text), Revision: 1, Language: "en",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := coordinator.WaitForVidRush(context.Background(), runID); err != nil {
			t.Fatal(err)
		}
	}

	run("replay-run-1")
	if got := semantic.calls.Load(); got != 1 {
		t.Fatalf("first semantic calls = %d, want 1", got)
	}
	if got := provider.searches.Load(); got != 1 {
		t.Fatalf("first discovery calls = %d, want 1", got)
	}
	if got := materializer.downloads.Load(); got != 1 {
		t.Fatalf("first downloads = %d, want 1", got)
	}
	if got := materializer.uploads.Load(); got != 1 {
		t.Fatalf("first uploads = %d, want 1", got)
	}

	run("replay-run-2")
	if got := semantic.calls.Load(); got != 1 {
		t.Fatalf("replay semantic calls = %d, want unchanged", got)
	}
	if got := provider.searches.Load(); got != 1 {
		t.Fatalf("replay discovery calls = %d, want unchanged", got)
	}
	if got := materializer.downloads.Load(); got != 1 {
		t.Fatalf("replay downloads = %d, want unchanged", got)
	}
	if got := materializer.uploads.Load(); got != 1 {
		t.Fatalf("replay uploads = %d, want unchanged", got)
	}
}

var _ scriptports.VidRushCachePort = (*memoryVidRushCache)(nil)
