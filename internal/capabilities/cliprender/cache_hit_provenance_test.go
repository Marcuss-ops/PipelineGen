package cliprender

// cache_hit_provenance_test.go — ticket CR-19-07: a cache hit must not claim
// provenance it does not have.
//
// The historical result synthesized a Prepared with zero timings and a plan
// whose "plan_sha256" was the REQUEST fingerprint. That reported a measured
// zero for preparation that never ran and a plan digest for a plan that was
// never sealed. The result now omits both blocks and says cache_hit.

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestCachedCompletionReportsNoFabricatedProvenance(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-cached"}
	req.Normalize()
	fp, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	cache := newMemoryRenderCache()
	if err := cache.Put(context.Background(), &RenderCacheRecord{
		Fingerprint: fp,
		AssetID:     "asset_published",
		StorageKey:  "store/clip.mp4",
		ArtifactURL: "http://store/clip.mp4",
		ContentType: "video/mp4",
		SHA256:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SizeBytes:   2048,
		DurationSec: 4,
		Width:       1920,
		Height:      1080,
		FPSNum:      24,
		FPSDen:      1,
		Backend:     BackendChrononVulkan,
	}); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	worker := &Worker{log: zap.NewNop(), renderCache: cache}
	result, err := worker.Handle(context.Background(), &job.Job{ID: "job-cached", Payload: payload}, nil)
	if err != nil {
		t.Fatalf("cache-hit handle: %v", err)
	}

	if hit, _ := result["cache_hit"].(bool); !hit {
		t.Fatalf("cache_hit = %v, want true", result["cache_hit"])
	}
	if _, ok := result["timings"]; ok {
		t.Fatalf("a cache hit must not report preparation timings, got %v", result["timings"])
	}
	if _, ok := result["plan"]; ok {
		t.Fatalf("a cache hit must not report a sealed plan, got %v", result["plan"])
	}
	// The contract is the renderer's media contract. A cache hit resolved none,
	// so reporting contract_id (historically the BACKEND NAME) and an all-zero
	// contract block would describe a contract the renderer was never held to.
	if _, ok := result["contract_id"]; ok {
		t.Fatalf("a cache hit must not report a contract_id, got %v", result["contract_id"])
	}
	if _, ok := result["contract"]; ok {
		t.Fatalf("a cache hit must not report a contract block, got %v", result["contract"])
	}
	asset, ok := result["asset"].(map[string]any)
	if !ok {
		t.Fatalf("asset block = %v, want the reused publication", result["asset"])
	}
	if asset["publication_status"] != "REUSED" {
		t.Fatalf("publication_status = %v, want REUSED", asset["publication_status"])
	}
	if asset["asset_id"] != "asset_published" {
		t.Fatalf("asset_id = %v, want the cached asset", asset["asset_id"])
	}
}
