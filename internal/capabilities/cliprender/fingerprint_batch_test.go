package cliprender

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestFingerprint_DeterministicAndNormalized verifies the stability
// contract: same normalized request → same fingerprint, byte-identical
// across handler and worker (both Normalize before hashing).
func TestFingerprint_DeterministicAndNormalized(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1"}
	req.Normalize()
	fp1, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	fp2, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint second call: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint not deterministic: %q vs %q", fp1, fp2)
	}
	if len(fp1) != 64 {
		t.Fatalf("fingerprint length: got %d, want 64", len(fp1))
	}
	// A different source must change the digest.
	other := &RenderRequest{SourceAssetID: "yt_ERzbkt5r5Gg_32_66_v1"}
	other.Normalize()
	fpOther, err := other.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint other: %v", err)
	}
	if fpOther == fp1 {
		t.Fatal("different source_asset_id must produce a different fingerprint")
	}
}

// TestFingerprint_ExcludesDestination ensures destination folder is not
// part of the canonical digest: same render published to two folders is
// still one render (the Drive outbox handles fan-out).
func TestFingerprint_ExcludesDestination(t *testing.T) {
	a := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{DriveFolderID: "folder-A"}}
	b := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{DriveFolderID: "folder-B"}}
	a.Normalize()
	b.Normalize()
	fpA, _ := a.Fingerprint()
	fpB, _ := b.Fingerprint()
	if fpA != fpB {
		t.Fatalf("destination must be excluded from fingerprint: %q vs %q", fpA, fpB)
	}
}

// TestBatchFingerprint_Deterministic verifies ordered clips → same batch id.
func TestBatchFingerprint_Deterministic(t *testing.T) {
	perClip := []string{"a", "b", "c"}
	b1 := BatchFingerprint(perClip)
	b2 := BatchFingerprint(perClip)
	if b1 != b2 {
		t.Fatalf("BatchFingerprint not deterministic: %q vs %q", b1, b2)
	}
	if len(b1) != 64 {
		t.Fatalf("batch fingerprint length: got %d, want 64", len(b1))
	}
	// Order matters.
	if BatchFingerprint([]string{"b", "a", "c"}) == b1 {
		t.Fatal("batch fingerprint must be order-sensitive")
	}
}

// TestRenderBatch_DedupIdenticalCollapsesToOne verifies N identical
// items in one batch collapse to one enqueue (fingerprint dedup).
func TestRenderBatch_DedupIdenticalCollapsesToOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// handler_batch dedup uses Fingerprint internally; stub returns same job
	// for first enqueue, second identical would be skipped entirely (no second Enqueue call).
	// We verify by counting Enqueue calls: stub only records last, so we use a counting stub.
	counting := &countingStubJobsSvc{returnJob: &job.Job{ID: "job_1"}}
	h2 := NewHandler(counting, zap.NewNop())
	r := gin.New()
	h2.RegisterRoutes(&r.RouterGroup)
	body := `{"items":[
		{"source_asset_id":"asset-1"},
		{"source_asset_id":"asset-1"},
		{"source_asset_id":"asset-1"}
	]}`
	req := httptest.NewRequest("POST", "/render/batch", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp batchRenderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode batch response: %v body=%s", err, rec.Body.String())
	}
	if resp.Accepted != 1 {
		t.Fatalf("accepted: got %d, want 1 (3 identical collapse to 1)", resp.Accepted)
	}
	if counting.calls != 1 {
		t.Fatalf("Enqueue calls: got %d, want 1", counting.calls)
	}
	if len(resp.Jobs) != 3 {
		t.Fatalf("jobs len: got %d, want 3", len(resp.Jobs))
	}
	// All three positions must share the same job_id.
	if resp.Jobs[0].JobID != resp.Jobs[1].JobID || resp.Jobs[1].JobID != resp.Jobs[2].JobID {
		t.Fatalf("identical items must share job_id: %+v", resp.Jobs)
	}
}

// TestRenderBatch_DistinctItemsGetDistinctJobs verifies distinct payloads
// never collapse: each gets its own correlation/job (the bug at 06:27 where
// 3 distinct clips reused one job due to shared X-Request-ID).
func TestRenderBatch_DistinctItemsGetDistinctJobs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	counting := &countingStubJobsSvc{returnJob: &job.Job{ID: "job_X"}}
	// counting stub returns a distinct ID per call so we can assert distinctness.
	countingGen := &distinctIDStub{base: "job_"}
	h := NewHandler(countingGen, zap.NewNop())
	r := gin.New()
	h.RegisterRoutes(&r.RouterGroup)
	body := `{"items":[
		{"source_asset_id":"yt_0ElQTzSx3ec_72_91_v1"},
		{"source_asset_id":"yt_ERzbkt5r5Gg_32_66_v1"},
		{"source_asset_id":"yt_Gcgdk1gEo8U_285_302_v1"}
	]}`
	req := httptest.NewRequest("POST", "/render/batch", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp batchRenderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Accepted != 3 {
		t.Fatalf("accepted: got %d, want 3", resp.Accepted)
	}
	if counting.calls != 0 { // unused, keep linter happy
		t.Logf("counting stub not used")
	}
	seen := make(map[string]struct{})
	for _, j := range resp.Jobs {
		if j.Status == "CACHED" {
			t.Fatalf("unexpected CACHED on cold batch: %+v", j)
		}
		seen[j.JobID] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("distinct items must get distinct job_ids: %+v", resp.Jobs)
	}
}

// TestRenderBatch_CacheHitReturnsCached verifies the handler-level cache
// hit path: a fingerprint already in the cache returns CACHED without enqueue.
func TestRenderBatch_CacheHitReturnsCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Pre-populate cache with one fingerprint.
	req := &RenderRequest{SourceAssetID: "asset-cached"}
	req.Normalize()
	fp, _ := req.Fingerprint()
	cache := newMemoryRenderCache()
	_ = cache.Put(nil, &RenderCacheRecord{Fingerprint: fp, AssetID: "asset_cached_1", StorageKey: "k1", ArtifactURL: "http://x/1", SHA256: "abc", SizeBytes: 123})
	svc := &stubJobsSvc{returnJob: &job.Job{ID: "job_new"}}
	h := NewHandler(svc, zap.NewNop()).WithRenderCache(cache)
	r := gin.New()
	h.RegisterRoutes(&r.RouterGroup)
	body := `{"items":[{"source_asset_id":"asset-cached"}]}`
	httpreq := httptest.NewRequest("POST", "/render/batch", bytes.NewReader([]byte(body)))
	httpreq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httpreq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp batchRenderResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Jobs[0].Status != "CACHED" || !resp.Jobs[0].CacheHit {
		t.Fatalf("expected CACHED hit: %+v", resp.Jobs[0])
	}
	if svc.enqueued != nil {
		t.Fatal("CACHED must not enqueue")
	}
}

type countingStubJobsSvc struct {
	returnJob *job.Job
	calls     int
	last      *job.EnqueueRequest
}

func (s *countingStubJobsSvc) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.calls++
	s.last = req
	if s.returnJob != nil {
		return s.returnJob, nil
	}
	return &job.Job{ID: "job_1"}, nil
}
func (s *countingStubJobsSvc) Get(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (s *countingStubJobsSvc) Cancel(_ context.Context, _ string) error { return nil }
func (s *countingStubJobsSvc) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *countingStubJobsSvc) IsTerminal(status job.Status) bool     { return status.IsTerminal() }
func (s *countingStubJobsSvc) RegisterHandler(_ string, _ any) error { return nil }
func (s *countingStubJobsSvc) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *countingStubJobsSvc) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}

type distinctIDStub struct {
	base string
	n    int
}

func (s *distinctIDStub) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	s.n++
	return &job.Job{ID: s.base + string(rune('0'+s.n))}, nil
}
func (s *distinctIDStub) Get(_ context.Context, _ string) (*job.Job, error) { return nil, nil }
func (s *distinctIDStub) Cancel(_ context.Context, _ string) error          { return nil }
func (s *distinctIDStub) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *distinctIDStub) IsTerminal(status job.Status) bool     { return status.IsTerminal() }
func (s *distinctIDStub) RegisterHandler(_ string, _ any) error { return nil }
func (s *distinctIDStub) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (s *distinctIDStub) Retry(_ context.Context, _ string) (*job.Job, error) { return nil, nil }

// Ensure stubs satisfy job.Service at compile time is checked in handler_test.go.
