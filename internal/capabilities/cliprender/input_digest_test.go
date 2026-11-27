package cliprender

import (
	"context"
	"errors"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// TestInputDigestChangesWithTranscriptRevision pins the CRA-03 acceptance: the
// same request (mode/language/persist unchanged) against a re-generated
// transcript must not validate the old cache row.
func TestInputDigestChangesWithTranscriptRevision(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-digest"}
	req.Normalize()

	before := InputDigestFromPrepared(req, &Prepared{
		Source:     &MaterializedAsset{SHA256: "aa"},
		Transcript: &TranscriptResult{TextSHA256: "text-v1"},
	})
	after := InputDigestFromPrepared(req, &Prepared{
		Source:     &MaterializedAsset{SHA256: "aa"},
		Transcript: &TranscriptResult{TextSHA256: "text-v2"},
	})
	if before == "" || after == "" {
		t.Fatal("input digest must never be empty for a certified input set")
	}
	if before == after {
		t.Fatal("transcript revision change did not change the input digest")
	}
}

func TestInputDigestChangesWithSourceAndOverlays(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-digest"}
	req.Normalize()
	base := InputDigestFromPrepared(req, &Prepared{
		Source:     &MaterializedAsset{SHA256: "source-a"},
		Transcript: &TranscriptResult{TextSHA256: "text"},
	})
	source := InputDigestFromPrepared(req, &Prepared{
		Source:     &MaterializedAsset{SHA256: "source-b"},
		Transcript: &TranscriptResult{TextSHA256: "text"},
	})
	if base == source {
		t.Fatal("source revision change did not change the input digest")
	}
	withOverlay := *req
	withOverlay.Overlays = []OverlayRefSpec{{RenderKey: "overlay-a"}}
	if got := InputDigestFromPrepared(&withOverlay, &Prepared{
		Source:     &MaterializedAsset{SHA256: "source-a"},
		Transcript: &TranscriptResult{TextSHA256: "text"},
	}); got == base {
		t.Fatal("overlay key change did not change the input digest")
	}
}

type fixedInputDigest string

func (f fixedInputDigest) InputDigest(context.Context, *RenderRequest) (string, error) {
	return string(f), nil
}

type failingInputDigest struct{ err error }

func (f failingInputDigest) InputDigest(context.Context, *RenderRequest) (string, error) {
	return "", f.err
}

// TestWorkerCacheHitRejectedWhenTranscriptChanged is the worker-level CRA-03
// acceptance: a fingerprint hit whose recorded input digest no longer matches
// the resolved transcript must NOT be served; the job falls through to a render.
func TestWorkerCacheHitRejectedWhenTranscriptChanged(t *testing.T) {
	ctx := context.Background()
	req := baseRenderRequest()
	fp, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	cache := newMemoryRenderCache()
	if err := cache.Put(ctx, &RenderCacheRecord{
		Fingerprint: fp,
		AssetID:     "stale-asset",
		SHA256:      "aa11",
		SizeBytes:   2048,
		Backend:     BackendChrononVulkan,
		InputSHA256: "transcript-v1",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	w := &Worker{renderCache: cache, inputRevisions: fixedInputDigest("transcript-v2"), log: zap.NewNop()}
	if _, hit := w.tryCachedCompletion(ctx, &job.Job{ID: "job-stale"}, req, time.Now(), func(int, string) {}); hit {
		t.Fatal("a changed input revision must not be served from the cache")
	}

	current := &Worker{renderCache: cache, inputRevisions: fixedInputDigest("transcript-v1"), log: zap.NewNop()}
	if _, hit := current.tryCachedCompletion(ctx, &job.Job{ID: "job-current"}, req, time.Now(), func(int, string) {}); !hit {
		t.Fatal("an unchanged input revision must still hit the cache")
	}
}

func TestCacheInputMatches(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-digest"}
	req.Normalize()
	rec := &RenderCacheRecord{InputSHA256: "abc"}

	if !cacheInputMatches(context.Background(), nil, req, rec) {
		t.Fatal("nil resolver must preserve the legacy key-only behaviour")
	}
	if !cacheInputMatches(context.Background(), fixedInputDigest("ABC"), req, rec) {
		t.Fatal("matching digest (case-insensitive) must be accepted")
	}
	if cacheInputMatches(context.Background(), fixedInputDigest("def"), req, rec) {
		t.Fatal("changed input revision must reject the cache hit")
	}
	// A record without a digest cannot certify its inputs. The lowering version
	// bump makes pre-digest rows unreachable, so the fail-closed answer is a
	// miss rather than a legacy compatibility hit.
	if cacheInputMatches(context.Background(), fixedInputDigest("abc"), req, &RenderCacheRecord{}) {
		t.Fatal("a record without an input digest must miss, not serve unproven bytes")
	}
	if cacheInputMatches(context.Background(), fixedInputDigest("abc"), req, nil) {
		t.Fatal("a missing cache record must miss")
	}
	if cacheInputMatches(context.Background(), failingInputDigest{errors.New("lookup failed")}, req, rec) {
		t.Fatal("an unresolvable revision must fail closed (miss), not serve stale bytes")
	}
}
