package cliprender

// input_revision_test.go pins ticket CRA-03: inputs are identified by revision
// when the request pins one, and the pin is verified against the bytes the
// preparation actually resolved. A changed transcript therefore misses the
// cache and renders again instead of serving stale subtitles.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const (
	revisionA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	revisionB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func transcriptRevisionRequest(transcriptRevision string) *RenderRequest {
	req := baseRenderRequest()
	req.Transcript.Revision = transcriptRevision
	req.Normalize()
	return req
}

func revisionRequest(transcriptRevision string) *RenderRequest {
	req := baseRenderRequest()
	req.SourceRevision = revisionA
	req.Background = &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "asset-bg", Revision: revisionA}
	req.Watermark = &WatermarkSpec{Enabled: true, AssetID: "asset-wm", Revision: revisionA}
	req.Transcript.Revision = transcriptRevision
	req.Normalize()
	return req
}

func TestFingerprint_InputRevisionsAreIdentity(t *testing.T) {
	base := revisionRequest(revisionA)
	baseFP, err := base.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	// A sha256: prefix is a spelling of the same digest, not a new revision.
	prefixed := revisionRequest("sha256:" + strings.ToUpper(revisionA))
	prefixedFP, err := prefixed.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(prefixed): %v", err)
	}
	if prefixedFP != baseFP {
		t.Fatalf("sha256: prefix changed the fingerprint: %s != %s", prefixedFP, baseFP)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*RenderRequest)
	}{
		{"source", func(r *RenderRequest) { r.SourceRevision = revisionB }},
		{"background", func(r *RenderRequest) { r.Background.Revision = revisionB }},
		{"watermark", func(r *RenderRequest) { r.Watermark.Revision = revisionB }},
		{"transcript", func(r *RenderRequest) { r.Transcript.Revision = revisionB }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := revisionRequest(revisionA)
			tc.mutate(changed)
			fp, err := changed.Fingerprint()
			if err != nil {
				t.Fatalf("Fingerprint: %v", err)
			}
			if fp == baseFP {
				t.Fatalf("changing %s revision did not change the fingerprint", tc.name)
			}
		})
	}
}

func TestValidate_RejectsMalformedRevisionPin(t *testing.T) {
	req := baseRenderRequest()
	req.SourceRevision = "not-a-sha256"
	req.Normalize()
	if err := req.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Validate = %v, want ErrInvalidRequest", err)
	}
}

func TestPrepare_BackgroundRevisionMismatchFailsClosed(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
		"asset-bg":     {AssetID: "asset-bg", MediaType: "image"},
	})
	mat := &fakeMaterializer{}
	tr := &fakeTranscriptResolver{
		existing:   &TranscriptResult{Language: "en", Text: "existing", TextSHA256: sha256Hex("existing")},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, mat, tr)

	req := baseRenderRequest()
	req.Background = &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "asset-bg", Revision: revisionB}
	req.Normalize()
	if _, err := preparer.Prepare(context.Background(), req, "run-bg-mismatch"); !errors.Is(err, ErrInputRevisionMismatch) {
		t.Fatalf("Prepare = %v, want ErrInputRevisionMismatch", err)
	}

	// The same request pinned to the bytes the materializer actually produced
	// passes: the gate rejects a disagreement, not the use of pins.
	req.Background.Revision = sha256Hex("asset-bg")
	if _, err := preparer.Prepare(context.Background(), req, "run-bg-match"); err != nil {
		t.Fatalf("Prepare with matching revision: %v", err)
	}
}

func TestPrepare_TranscriptRevisionMismatchFailsClosed(t *testing.T) {
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	tr := &fakeTranscriptResolver{
		existing:   &TranscriptResult{Language: "en", Text: "new transcript", TextSHA256: revisionB},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, &fakeMaterializer{}, tr)

	req := baseRenderRequest()
	req.Transcript.Revision = revisionA
	req.Normalize()
	if _, err := preparer.Prepare(context.Background(), req, "run-tr-mismatch"); !errors.Is(err, ErrInputRevisionMismatch) {
		t.Fatalf("Prepare = %v, want ErrInputRevisionMismatch", err)
	}
}

func TestTranscriptRevisionChangeMissesCacheAndRendersAgain(t *testing.T) {
	ctx := context.Background()
	w, mat, _ := newTestWorker(t)
	tr := &fakeTranscriptResolver{
		existing: &TranscriptResult{
			Language:   "en",
			Text:       "new transcript",
			Cues:       []Cue{{StartMs: 0, EndMs: 1000, Text: "new transcript"}},
			TextSHA256: revisionB,
			Reused:     true,
		},
		existingOK: true,
	}
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source", ContentSHA256: sha256Hex("asset-source")},
	})
	w.preparer = newTestPreparer(resolver, mat, tr)
	// Keep the CRA-03 digest guard pointed at the replaced preparer, exactly as
	// NewWorker wires it in production.
	w.WithInputDigestResolver(w.preparer)

	cache := newMemoryRenderCache()
	renderer := &fakeAsyncRenderExecutor{outcome: fullRenderOutcome()}
	w.WithRenderCache(cache).WithRenderExecutor(renderer)

	pinnedA := transcriptRevisionRequest(revisionA)
	fpA, err := pinnedA.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(A): %v", err)
	}
	if err := cache.Put(ctx, &RenderCacheRecord{
		Fingerprint: fpA,
		AssetID:     "asset-published-a",
		SHA256:      revisionA,
		SizeBytes:   2048,
		DurationSec: 4,
		Backend:     BackendChrononVulkan,
	}); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	pinnedB := transcriptRevisionRequest(revisionB)
	fpB, err := pinnedB.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(B): %v", err)
	}
	if fpB == fpA {
		t.Fatal("transcript revision is not part of the identity")
	}
	if _, err := cache.Get(ctx, fpB); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("cache Get(B) = %v, want ErrCacheMiss", err)
	}

	result, _, err := handleSubmitted(t, ctx, w, "job-revision-b", pinnedB)
	if err != nil {
		t.Fatalf("submit with changed transcript: %v", err)
	}
	if hit, _ := result["cache_hit"].(bool); hit {
		t.Fatal("a changed transcript revision must not be served from the cache")
	}
	if renderer.submitCalls != 1 {
		t.Fatalf("submit calls = %d, want 1 (a new render)", renderer.submitCalls)
	}

	// Identical request + unchanged inputs keep hitting: put the new record with
	// the CURRENT input digest and replay the same request.
	digestB, err := w.preparer.InputDigest(ctx, pinnedB)
	if err != nil {
		t.Fatalf("input digest B: %v", err)
	}
	if err := cache.Put(ctx, &RenderCacheRecord{
		Fingerprint: fpB,
		AssetID:     "asset-published-b",
		InputSHA256: digestB,
		SHA256:      revisionB,
		SizeBytes:   2048,
		DurationSec: 4,
		Backend:     BackendChrononVulkan,
	}); err != nil {
		t.Fatalf("cache put B: %v", err)
	}
	raw, err := json.Marshal(pinnedB)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	hitResult, err := w.Handle(ctx, &job.Job{ID: "job-revision-b-hit", Payload: raw}, nil)
	if err != nil {
		t.Fatalf("cache-hit handle: %v", err)
	}
	if hit, _ := hitResult["cache_hit"].(bool); !hit {
		t.Fatal("identical request with unchanged inputs must still hit the cache")
	}
	if renderer.submitCalls != 1 {
		t.Fatalf("cache hit submitted %d renders, want the original 1", renderer.submitCalls)
	}
}
