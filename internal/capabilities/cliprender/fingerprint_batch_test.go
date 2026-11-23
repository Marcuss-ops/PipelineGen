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

// TestFingerprint_IncludesDestinationFolder pins ticket CR-19-03: the Drive
// destination is part of the identity, because a cache hit returns the
// previously PUBLISHED asset. Excluding the folder let a request be served the
// artifact that was delivered somewhere else, silently.
func TestFingerprint_IncludesDestinationFolder(t *testing.T) {
	a := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{DriveFolderID: "folder-A"}}
	b := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{DriveFolderID: "folder-B"}}
	a.Normalize()
	b.Normalize()
	fpA, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint A: %v", err)
	}
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint B: %v", err)
	}
	if fpA == fpB {
		t.Fatalf("destination folder must distinguish delivery lineages: %q", fpA)
	}
	// The same folder must still be the same identity: the digest separates
	// deliveries without making every submission unique.
	c := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{DriveFolderID: "folder-A"}}
	c.Normalize()
	fpC, err := c.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint C: %v", err)
	}
	if fpC != fpA {
		t.Fatalf("same destination must be the same identity: %q vs %q", fpC, fpA)
	}
}

func TestFingerprint_IncludesDestinationSubfolder(t *testing.T) {
	a := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{SubfolderName: "Dolly Parton Run A"}}
	b := &RenderRequest{SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1", Destination: &DestinationSpec{SubfolderName: "Dolly Parton Run B"}}
	a.Normalize()
	b.Normalize()
	fpA, err := a.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint A: %v", err)
	}
	fpB, err := b.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint B: %v", err)
	}
	if fpA == fpB {
		t.Fatalf("destination subfolder must distinguish delivery lineages: %q", fpA)
	}
}

// classicPaleOliveRequest builds the canonical day-1 benchmark request: the
// Pale Olive Classic (`classic1`) video plate behind a burned subtitle track
// and a top-right text watermark, rendered under a full-GPU execution demand.
func classicPaleOliveRequest() *RenderRequest {
	return &RenderRequest{
		SourceAssetID: "yt_0ElQTzSx3ec_72_91_v1",
		Background:    &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "classic1", Kind: BackgroundKindVideo},
		Subtitles:     &SubtitlesSpec{Enabled: true, Mode: SubtitlesModeBurn},
		Watermark:     &WatermarkSpec{Enabled: true, Text: "VeloxEditing", Position: PositionTopRight},
		Execution:     &ExecutionSpec{RequireGPU: true},
	}
}

// TestFingerprint_ExecutionGPURequirementIsNotIdentity pins the CR-01 choice:
// execution is a policy/assertion, not a pixel input. Chronon is the only
// backend and is GPU-native, so both requests address the same render bytes;
// require_zero_copy remains unsatisfiable and is rejected by Validate before
// it can reach the cache.
func TestFingerprint_ExecutionGPURequirementIsNotIdentity(t *testing.T) {
	plain := classicPaleOliveRequest()
	plain.Execution = &ExecutionSpec{}
	plain.Normalize()
	fpPlain, err := plain.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint plain: %v", err)
	}

	gpu := classicPaleOliveRequest()
	gpu.Normalize()
	fpGPU, err := gpu.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint gpu: %v", err)
	}
	if fpPlain != fpGPU {
		t.Fatalf("execution.require_gpu must not split render identity: %q vs %q", fpPlain, fpGPU)
	}

	// require_zero_copy is UNSATISFIABLE (no backend certifies video_zero_copy)
	// and is rejected at the request gate (Validate), so it can never reach the
	// render cache at all because Validate rejects it at the request gate.
	// Fingerprint itself no longer repeats that validation (CR-07).
	zeroCopy := classicPaleOliveRequest()
	zeroCopy.Execution = &ExecutionSpec{RequireZeroCopy: true}
	zeroCopy.Normalize()
	if _, err := zeroCopy.Fingerprint(); err != nil {
		t.Fatalf("Fingerprint must not repeat validation: %v", err)
	}
	if err := zeroCopy.Validate(); err == nil {
		t.Fatal("Validate must reject execution.require_zero_copy")
	}
}

// TestFingerprint_ClassicPaleOliveScenarioStable pins the exact benchmark
// scenario (classic1 Pale Olive video plate + burned subtitles + top-right
// text watermark + full GPU) to one stable digest. Two independent
// derivations are compared: a Go literal and a JSON document whose keys are
// reordered. Key order on the wire is not semantic, so both must address the
// same bytes — and the digest must survive a full decode→encode→decode round
// trip, which is what lets the handler and the worker agree on a cache key
// after the payload has been persisted and re-read from the queue.
func TestFingerprint_ClassicPaleOliveScenarioStable(t *testing.T) {
	// Deliberately reordered keys, and margin_px/opacity omitted so Normalize
	// has to apply the same non-zero defaults the Go literal gets.
	const body = `{
		"watermark": {"position": "top_right", "text": "VeloxEditing", "enabled": true},
		"subtitles": {"mode": "burn", "enabled": true},
		"execution": {"require_gpu": true},
		"background": {"kind": "video", "asset_id": "classic1", "mode": "asset"},
		"source_asset_id": "yt_0ElQTzSx3ec_72_91_v1"
	}`
	var decoded RenderRequest
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode scenario body: %v", err)
	}
	decoded.Normalize()
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded scenario must validate: %v", err)
	}

	literal := classicPaleOliveRequest()
	literal.Normalize()

	fpDecoded, err := decoded.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint decoded: %v", err)
	}
	fpLiteral, err := literal.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint literal: %v", err)
	}
	if fpDecoded != fpLiteral {
		t.Fatalf("wire key order must not change the fingerprint: decoded %q vs literal %q", fpDecoded, fpLiteral)
	}
	if len(fpDecoded) != 64 {
		t.Fatalf("fingerprint length: got %d, want 64", len(fpDecoded))
	}

	// Persisted-payload round trip must be a fixed point: the worker re-decodes
	// the same RenderRequest from the queue and probes the same cache key.
	raw, err := json.Marshal(&decoded)
	if err != nil {
		t.Fatalf("re-encode scenario: %v", err)
	}
	var reDecoded RenderRequest
	if err := json.Unmarshal(raw, &reDecoded); err != nil {
		t.Fatalf("re-decode scenario: %v", err)
	}
	reDecoded.Normalize()
	fpRoundTrip, err := reDecoded.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint round trip: %v", err)
	}
	if fpRoundTrip != fpDecoded {
		t.Fatalf("fingerprint must survive a wire round trip: %q vs %q", fpRoundTrip, fpDecoded)
	}

	// A drifted plate must NOT reuse the certified bytes of another plate.
	drifted := classicPaleOliveRequest()
	drifted.Background = &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "classic2", Kind: BackgroundKindVideo}
	drifted.Normalize()
	fpDrifted, err := drifted.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint drifted: %v", err)
	}
	if fpDrifted == fpDecoded {
		t.Fatal("a different background plate must not reuse the same fingerprint")
	}

	// And the watermark side is part of the identity too.
	movedWatermark := classicPaleOliveRequest()
	movedWatermark.Watermark = &WatermarkSpec{Enabled: true, Text: "VeloxEditing", Position: PositionTopLeft}
	movedWatermark.Normalize()
	fpMoved, err := movedWatermark.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint moved watermark: %v", err)
	}
	if fpMoved == fpDecoded {
		t.Fatal("moving the watermark from top_right must not reuse the same fingerprint")
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
	if resp.Accepted != 3 {
		t.Fatalf("accepted: got %d, want 3 positions (3 identical share one job)", resp.Accepted)
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
	_ = cache.Put(context.Background(), &RenderCacheRecord{Fingerprint: fp, AssetID: "asset_cached_1", StorageKey: "k1", ArtifactURL: "http://x/1", SHA256: "abc", SizeBytes: 123, DeliveryStatus: "pending", DriveFileID: "drive-1"})
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
	if resp.Jobs[0].JobID != "" {
		t.Fatalf("a cache hit is not a job, got job_id %q", resp.Jobs[0].JobID)
	}
	if resp.Jobs[0].AssetID != "asset_cached_1" {
		t.Fatalf("a cache hit must identify the reused asset, got %+v", resp.Jobs[0])
	}
	if resp.Jobs[0].DeliveryStatus != "pending" || !resp.Jobs[0].DeliveryPending || resp.Jobs[0].DriveFileID != "drive-1" {
		t.Fatalf("cache hit must expose delivery state, got %+v", resp.Jobs[0])
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

// TestFingerprint_PinsTheLoweringVersion makes the renderer lowering part of the
// render identity. A change in the overlay lowering that alters the produced
// pixels must bump RendererLoweringVersion and update this golden in the same
// change; otherwise a cache hit serves bytes rendered by the previous code for
// an identical payload, which is exactly the intermittency the version exists to
// remove.
func TestFingerprint_PinsTheLoweringVersion(t *testing.T) {
	if RendererLoweringVersion == "" {
		t.Fatal("RendererLoweringVersion must never be empty: the fingerprint would stop tracking the lowering")
	}
	req := &RenderRequest{SourceAssetID: "asset-lowering-pin"}
	req.Normalize()
	fp, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	const golden = "abd56d5f35817ab0b65fd7ab39749d58feeab193c3679d91ebc91fdd2d665c29"
	if fp != golden {
		t.Fatalf("render identity = %q, want %q; update the golden together with RendererLoweringVersion", fp, golden)
	}
}
