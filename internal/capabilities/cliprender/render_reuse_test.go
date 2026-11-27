package cliprender

import (
	"context"
	"errors"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// TestTryRenderReuse_DeliversWithoutRender pins CRA-04: destination B misses the
// delivery-aware fingerprint but hits the render identity, so the job publishes
// the SAME certified bytes to B without preparing or rendering, and writes B's
// own cache row. A's row is untouched.
func TestTryRenderReuse_DeliversWithoutRender(t *testing.T) {
	reqA := &RenderRequest{SourceAssetID: "asset-reuse"}
	reqA.Normalize()
	reqA.Destination.DriveFolderID = "folder-A"
	reqB := *reqA
	destB := *reqA.Destination
	reqB.Destination = &destB
	destB.DriveFolderID = "folder-B"

	fpA, err := reqA.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(A): %v", err)
	}
	fpB, err := reqB.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(B): %v", err)
	}
	renderFP, err := reqA.RenderFingerprint()
	if err != nil {
		t.Fatalf("RenderFingerprint: %v", err)
	}
	if fpA == fpB {
		t.Fatal("fixture invalid: destinations must have distinct delivery fingerprints")
	}

	cache := newMemoryRenderCache()
	if err := cache.Put(context.Background(), &RenderCacheRecord{
		Fingerprint:       fpA,
		RenderFingerprint: renderFP,
		InputSHA256:       "input-digest-v1",
		SourceTitle:       "Certified Clip",
		Language:          "it",
		AssetID:           "cliprender_asset_a",
		StorageKey:        "renders/a.mp4",
		ArtifactURL:       "https://objectstore/renders/a.mp4",
		ContentType:       "video/mp4",
		SHA256:            "aa11",
		SizeBytes:         2048,
		DurationSec:       3,
		Width:             1920,
		Height:            1080,
		FPSNum:            24,
		FPSDen:            1,
		Backend:           BackendChrononVulkan,
		DeliveryStatus:    "completed",
		DriveFileID:       "drive-file-A",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	pub := &fakeRenderPublisher{out: RenderPublishResult{
		AssetID:        "cliprender_asset_b",
		DriveFileID:    "drive-file-B",
		DeliveryStatus: "pending",
		SizeBytes:      2048,
	}}
	w := &Worker{
		renderCache:    cache,
		publisher:      pub,
		inputRevisions: fixedInputDigest("input-digest-v1"),
		log:            zap.NewNop(),
	}
	res, ok, err := w.tryRenderReuse(context.Background(), &job.Job{ID: "job-B"}, &reqB, "folder-B", time.Now(), func(int, string) {})
	if err != nil {
		t.Fatalf("tryRenderReuse(B): %v", err)
	}
	if !ok {
		t.Fatal("expected the certified render to be reused for the second destination")
	}
	if pub.called != 1 {
		t.Fatalf("publisher calls = %d, want 1", pub.called)
	}
	if pub.input.DriveFolderID != "folder-B" {
		t.Fatalf("published to %q, want folder-B", pub.input.DriveFolderID)
	}
	if pub.input.Outcome.OutputPath != "" {
		t.Fatalf("reuse must stream from the certified locator, got local path %q", pub.input.Outcome.OutputPath)
	}
	if pub.input.CertifiedSHA256 != "aa11" || pub.input.CertifiedSizeBytes != 2048 {
		t.Fatalf("reuse must certify the cached bytes, got sha=%q size=%d", pub.input.CertifiedSHA256, pub.input.CertifiedSizeBytes)
	}
	if res["render_reused"] != true || res["cache_hit"] != true {
		t.Fatalf("result must report the reuse honestly, got %v", res)
	}

	recB, err := cache.Get(context.Background(), fpB)
	if err != nil {
		t.Fatalf("destination B cache row missing: %v", err)
	}
	if recB.AssetID != "cliprender_asset_b" || recB.DriveFileID != "drive-file-B" {
		t.Fatalf("B row = asset %q drive %q, want B's own locator", recB.AssetID, recB.DriveFileID)
	}
	if recB.RenderFingerprint != renderFP || recB.InputSHA256 != "input-digest-v1" {
		t.Fatalf("B row lost the shared render identity/input digest: %+v", recB)
	}
	recA, err := cache.Get(context.Background(), fpA)
	if err != nil {
		t.Fatalf("destination A cache row disappeared: %v", err)
	}
	if recA.AssetID != "cliprender_asset_a" || recA.DriveFileID != "drive-file-A" {
		t.Fatalf("delivery to B mutated A: asset %q drive %q", recA.AssetID, recA.DriveFileID)
	}
	if recA.DeliveryStatus != "completed" || recB.DeliveryStatus != "pending" {
		t.Fatalf("delivery statuses are not independent: A=%q B=%q", recA.DeliveryStatus, recB.DeliveryStatus)
	}

	// Third destination: still no second render.
	reqC := *reqA
	destC := *reqA.Destination
	reqC.Destination = &destC
	destC.DriveFolderID = "folder-C"
	fpC, err := reqC.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint(C): %v", err)
	}
	pub.out = RenderPublishResult{AssetID: "cliprender_asset_c", DriveFileID: "drive-file-C", DeliveryStatus: "pending", SizeBytes: 2048}
	if _, ok, err := w.tryRenderReuse(context.Background(), &job.Job{ID: "job-C"}, &reqC, "folder-C", time.Now(), func(int, string) {}); err != nil || !ok {
		t.Fatalf("third destination must also reuse the certified render: ok=%v err=%v", ok, err)
	}
	if pub.called != 2 {
		t.Fatalf("publisher calls after three destinations = %d, want 2 (one render, every later delivery reuses)", pub.called)
	}
	recC, err := cache.Get(context.Background(), fpC)
	if err != nil {
		t.Fatalf("destination C cache row missing: %v", err)
	}
	if recC.AssetID != "cliprender_asset_c" || recC.DriveFileID != "drive-file-C" {
		t.Fatalf("C row = asset %q drive %q, want C's own locator", recC.AssetID, recC.DriveFileID)
	}
}

// TestTryRenderReuse_PropagatesPublishFailure pins the distinction between a
// pre-condition miss (fall back to a render) and a publish failure (surface it:
// the delivery may already be committed, so rendering again would duplicate it).
func TestTryRenderReuse_PropagatesPublishFailure(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-reuse"}
	req.Normalize()
	req.Destination.DriveFolderID = "folder-A"
	renderFP, _ := req.RenderFingerprint()
	fp, _ := req.Fingerprint()

	cache := newMemoryRenderCache()
	_ = cache.Put(context.Background(), &RenderCacheRecord{
		Fingerprint:       fp,
		RenderFingerprint: renderFP,
		InputSHA256:       "digest",
		SourceTitle:       "Certified Clip",
		AssetID:           "cliprender_asset_a",
		ArtifactURL:       "https://objectstore/renders/a.mp4",
		SHA256:            "aa11",
		SizeBytes:         2048,
		Backend:           BackendChrononVulkan,
	})
	pub := &fakeRenderPublisher{err: errors.New("drive commit failed")}
	w := &Worker{renderCache: cache, publisher: pub, inputRevisions: fixedInputDigest("digest"), log: zap.NewNop()}
	if _, ok, err := w.tryRenderReuse(context.Background(), &job.Job{ID: "job"}, req, "folder-B", time.Now(), func(int, string) {}); err == nil {
		t.Fatal("publish failure must be propagated, not silently turned into a re-render")
	} else if ok {
		t.Fatal("a failed delivery must not report a reuse success")
	}
}

func TestTryRenderReuse_FailsClosedOnChangedInputs(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-reuse"}
	req.Normalize()
	req.Destination.DriveFolderID = "folder-A"
	renderFP, _ := req.RenderFingerprint()
	fp, _ := req.Fingerprint()

	cache := newMemoryRenderCache()
	_ = cache.Put(context.Background(), &RenderCacheRecord{
		Fingerprint:       fp,
		RenderFingerprint: renderFP,
		InputSHA256:       "old-digest",
		SourceTitle:       "Certified Clip",
		Language:          "it",
		AssetID:           "cliprender_asset_a",
		ArtifactURL:       "https://objectstore/renders/a.mp4",
		SHA256:            "aa11",
		SizeBytes:         2048,
		Backend:           BackendChrononVulkan,
	})
	pub := &fakeRenderPublisher{}
	w := &Worker{
		renderCache:    cache,
		publisher:      pub,
		inputRevisions: fixedInputDigest("new-digest"),
		log:            zap.NewNop(),
	}
	if _, ok, _ := w.tryRenderReuse(context.Background(), &job.Job{ID: "job"}, req, "folder-B", time.Now(), func(int, string) {}); ok {
		t.Fatal("changed input revision must fall back to a render, not reuse stale bytes")
	}
	if pub.called != 0 {
		t.Fatalf("publisher called %d times on a rejected reuse", pub.called)
	}
}
