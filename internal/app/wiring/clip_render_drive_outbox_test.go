package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

type outboxTestPublisher struct {
	mu       sync.Mutex
	requests []delivery.PublishRequest
}

func (p *outboxTestPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	return &delivery.PublishResult{
		FileID:       "file-" + req.Filename,
		WebViewLink:  "https://drive.google.com/file/d/file-" + req.Filename + "/view",
		DownloadLink: "https://drive.google.com/uc?id=file-" + req.Filename,
	}, nil
}

func (p *outboxTestPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	return req.DestinationFolderID, nil
}

func (p *outboxTestPublisher) all() []delivery.PublishRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]delivery.PublishRequest(nil), p.requests...)
}

// outboxTestMutator records the reconcile/patch calls. The embedded interface
// is never exercised (only the two self-owned methods are).
type outboxTestMutator struct {
	persistence.AssetMutator
	reconciled []persistence.DriveLocationPatch
	patches    []persistence.AssetPatch
}

func (m *outboxTestMutator) ReconcileDriveLocations(_ context.Context, changes []persistence.DriveLocationPatch) error {
	m.reconciled = append(m.reconciled, changes...)
	return nil
}

func (m *outboxTestMutator) PatchAsset(_ context.Context, patch persistence.AssetPatch) error {
	m.patches = append(m.patches, patch)
	return nil
}

type outboxTestSubtitles struct {
	detail.SubtitleArtifactRepository
	upserted []detail.SubtitleArtifact
}

func (r *outboxTestSubtitles) Upsert(_ context.Context, art *detail.SubtitleArtifact) error {
	r.upserted = append(r.upserted, *art)
	return nil
}

func clipDeliveryFixture(t *testing.T) (cliprender.ClipRenderDriveDeliveryRequest, string, string) {
	t.Helper()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(videoPath, []byte("rendered-video-bytes"), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	sidecarPath := filepath.Join(dir, "clip.ass")
	if err := os.WriteFile(sidecarPath, []byte("[Script Info]\nTitle: fixture\n"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	videoSHA, videoSize, err := digest.SHA256File(videoPath)
	if err != nil {
		t.Fatalf("hash video: %v", err)
	}
	sidecarSHA, sidecarSize, err := digest.SHA256File(sidecarPath)
	if err != nil {
		t.Fatalf("hash sidecar: %v", err)
	}
	return cliprender.ClipRenderDriveDeliveryRequest{
		SchemaVersion: "clip.render.drive_delivery.v1",
		AssetID:       "cliprender_deadbeef",
		RunID:         "run-1",
		SourceAssetID: "asset-src-1",
		LocalPath:     videoPath,
		Filename:      "Clip.mp4",
		FolderID:      "folder-leaf",
		ContentHash:   videoSHA,
		SizeBytes:     videoSize,
		Sidecar: &cliprender.ClipRenderSubtitleDelivery{
			LocalPath:    sidecarPath,
			Filename:     "Clip.ass",
			SHA256:       sidecarSHA,
			SizeBytes:    sidecarSize,
			LanguageCode: "en",
			StyleVersion: "v1",
		},
	}, videoPath, sidecarPath
}

// TestClipRenderDriveDeliveryHandler_DeliversSidecarBundle pins the bundle
// contract of the async clip.render Drive intent: the video and the optional
// ASS sidecar are uploaded by the SAME outbox consumer, the canonical subtitle
// artifact row is written, the asset metadata is flipped to completed, and both
// staged artifacts are removed.
func TestClipRenderDriveDeliveryHandler_DeliversSidecarBundle(t *testing.T) {
	payload, videoPath, sidecarPath := clipDeliveryFixture(t)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	pub := &outboxTestPublisher{}
	mut := &outboxTestMutator{}
	subs := &outboxTestSubtitles{}
	h, err := newClipRenderDriveDeliveryHandler(pub, mut, subs, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderDriveDeliveryHandler: %v", err)
	}
	claim := &pgmedia.OutboxClaim{Event: pgmedia.OutboxEvent{EventKey: "key-1", PayloadJSON: string(raw)}}
	if err := h.Handle(context.Background(), claim); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	reqs := pub.all()
	if len(reqs) != 2 {
		t.Fatalf("Drive uploads = %d, want 2 (video + sidecar)", len(reqs))
	}
	var videoReq, sidecarReq *delivery.PublishRequest
	for i := range reqs {
		switch {
		case reqs[i].Filename == payload.Filename:
			videoReq = &reqs[i]
		case reqs[i].Filename == payload.Sidecar.Filename:
			sidecarReq = &reqs[i]
		}
	}
	if videoReq == nil || sidecarReq == nil {
		t.Fatalf("uploads = %+v, want both the video and the sidecar", reqs)
	}
	if sidecarReq.ContentHash != payload.Sidecar.SHA256 || sidecarReq.DestinationFolderID != payload.FolderID {
		t.Errorf("sidecar upload = %+v, want content hash + folder from the intent", sidecarReq)
	}
	if len(mut.reconciled) != 1 || mut.reconciled[0].AssetID != payload.AssetID {
		t.Fatalf("reconciled = %+v, want one Drive location patch for %s", mut.reconciled, payload.AssetID)
	}
	if len(mut.patches) != 2 {
		t.Fatalf("asset patches = %d, want 2 (delivery completed + sidecar metadata)", len(mut.patches))
	}
	if len(subs.upserted) != 1 {
		t.Fatalf("subtitle artifact upserts = %d, want 1", len(subs.upserted))
	}
	art := subs.upserted[0]
	if art.AssetID != payload.SourceAssetID || art.Format != detail.SubtitleFormatASS ||
		art.Status != detail.SubtitleStatusReady || art.DriveFileID == "" || art.LanguageCode != "en" {
		t.Errorf("subtitle artifact = %+v, want a READY ASS row on %s", art, payload.SourceAssetID)
	}
	if _, err := os.Stat(videoPath); !os.IsNotExist(err) {
		t.Errorf("staged video still exists after delivery, err=%v", err)
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Errorf("staged sidecar still exists after delivery, err=%v", err)
	}
}

// clipVideoPayloadWithBytes builds a video-only delivery intent whose local
// artifact is exactly size bytes, so two calls model two renders of the SAME
// clip (same source asset + destination filename) with DIFFERENT bytes.
func clipVideoPayloadWithBytes(t *testing.T, size int) cliprender.ClipRenderDriveDeliveryRequest {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'r'}, size), 0o644); err != nil {
		t.Fatalf("write video: %v", err)
	}
	sha, onDisk, err := digest.SHA256File(path)
	if err != nil {
		t.Fatalf("hash video: %v", err)
	}
	return cliprender.ClipRenderDriveDeliveryRequest{
		SchemaVersion: "clip.render.drive_delivery.v1",
		AssetID:       "cliprender_" + sha[:24],
		RunID:         "run-rerender",
		SourceAssetID: "asset-src-1",
		LocalPath:     path,
		Filename:      "Clip.mp4",
		FolderID:      "folder-leaf",
		ContentHash:   sha,
		SizeBytes:     onDisk,
	}
}

// handleVideoDelivery runs the handler over payload and returns the upload
// request it produced for the video artifact.
func handleVideoDelivery(t *testing.T, payload cliprender.ClipRenderDriveDeliveryRequest) delivery.PublishRequest {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	pub := &outboxTestPublisher{}
	h, err := newClipRenderDriveDeliveryHandler(pub, &outboxTestMutator{}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderDriveDeliveryHandler: %v", err)
	}
	claim := &pgmedia.OutboxClaim{Event: pgmedia.OutboxEvent{EventKey: "key-" + payload.AssetID, PayloadJSON: string(raw)}}
	if err := h.Handle(context.Background(), claim); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	for _, req := range pub.all() {
		if req.Filename == payload.Filename {
			return req
		}
	}
	t.Fatalf("no upload request for %q; got %+v", payload.Filename, pub.all())
	return delivery.PublishRequest{}
}

// TestClipRenderDriveDeliveryHandler_RerenderOverwritesSameDriveFile pins the
// rerender regression: two renders of the same clip have different bytes (and
// therefore different content hashes / derived asset ids) but MUST derive the
// SAME upload idempotency key. That key is what the Drive uploader uses to find
// the file published one render earlier, so a drifted key silently downgrades
// ConflictOverwrite into "create a same-named duplicate".
func TestClipRenderDriveDeliveryHandler_RerenderOverwritesSameDriveFile(t *testing.T) {
	first := clipVideoPayloadWithBytes(t, 2048)
	second := clipVideoPayloadWithBytes(t, 4096)
	if first.AssetID == second.AssetID || first.ContentHash == second.ContentHash {
		t.Fatalf("fixture must model a rerender: asset id and hash must differ (%s/%s vs %s/%s)",
			first.AssetID, first.ContentHash, second.AssetID, second.ContentHash)
	}

	firstReq := handleVideoDelivery(t, first)
	secondReq := handleVideoDelivery(t, second)

	want := delivery.DeriveIdempotencyKey(
		delivery.DestinationClipMetadata,
		first.SourceAssetID+":"+first.Filename,
		cliprender.ClipRenderDrivePolicyVersion,
		1,
	)
	if firstReq.IdempotencyKey != want {
		t.Errorf("idempotency key = %q, want the logical clip identity %q", firstReq.IdempotencyKey, want)
	}
	if firstReq.IdempotencyKey != secondReq.IdempotencyKey {
		t.Errorf("rerender key drifted: %q (first render) vs %q (rerender) — ConflictOverwrite would create a same-named duplicate",
			firstReq.IdempotencyKey, secondReq.IdempotencyKey)
	}
	contentKey := delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, first.AssetID, first.ContentHash, 1)
	if firstReq.IdempotencyKey == contentKey {
		t.Error("upload identity must never fold in the content-addressed artifact (a rerender changes it)")
	}
	if firstReq.ConflictPolicy != delivery.ConflictOverwrite {
		t.Errorf("conflict policy = %v, want ConflictOverwrite", firstReq.ConflictPolicy)
	}
	if firstReq.DestinationFolderID != first.FolderID {
		t.Errorf("destination folder = %q, want %q", firstReq.DestinationFolderID, first.FolderID)
	}
}

// TestClipRenderDriveDeliveryHandler_BundleKeysAreLogicalIdentities pins the
// same identity rule for BOTH halves of the delivery bundle: the video and the
// ASS sidecar are keyed by (source asset, filename), never by the video digest
// or the ASS digest.
func TestClipRenderDriveDeliveryHandler_BundleKeysAreLogicalIdentities(t *testing.T) {
	payload, _, _ := clipDeliveryFixture(t)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	pub := &outboxTestPublisher{}
	h, err := newClipRenderDriveDeliveryHandler(pub, &outboxTestMutator{}, &outboxTestSubtitles{}, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderDriveDeliveryHandler: %v", err)
	}
	claim := &pgmedia.OutboxClaim{Event: pgmedia.OutboxEvent{EventKey: "key-bundle", PayloadJSON: string(raw)}}
	if err := h.Handle(context.Background(), claim); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	requests := pub.all()
	if len(requests) != 2 {
		t.Fatalf("Drive uploads = %d, want 2 (video + sidecar)", len(requests))
	}
	for _, req := range requests {
		want := delivery.DeriveIdempotencyKey(
			delivery.DestinationClipMetadata,
			payload.SourceAssetID+":"+req.Filename,
			cliprender.ClipRenderDrivePolicyVersion,
			1,
		)
		if req.IdempotencyKey != want {
			t.Errorf("upload %q idempotency key = %q, want %q", req.Filename, req.IdempotencyKey, want)
		}
	}
}

// TestClipRenderDriveDeliveryHandler_SidecarWithoutRegistryFailsClosed pins
// the fail-closed rule: a bundle that declares a sidecar while no subtitle
// artifact registry is wired must NOT silently drop the .ass.
func TestClipRenderDriveDeliveryHandler_SidecarWithoutRegistryFailsClosed(t *testing.T) {
	payload, _, _ := clipDeliveryFixture(t)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	pub := &outboxTestPublisher{}
	mut := &outboxTestMutator{}
	h, err := newClipRenderDriveDeliveryHandler(pub, mut, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderDriveDeliveryHandler: %v", err)
	}
	claim := &pgmedia.OutboxClaim{Event: pgmedia.OutboxEvent{EventKey: "key-2", PayloadJSON: string(raw)}}
	if err := h.Handle(context.Background(), claim); err == nil {
		t.Fatal("a declared sidecar without a subtitle registry must fail closed")
	}
}

// TestClipRenderDriveDeliveryHandler_IncompleteSidecarRejected pins the
// payload validation: a sidecar block missing its digest/size is a typed
// payload error, never a partial upload.
func TestClipRenderDriveDeliveryHandler_IncompleteSidecarRejected(t *testing.T) {
	payload, _, _ := clipDeliveryFixture(t)
	payload.Sidecar.SHA256 = ""
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	pub := &outboxTestPublisher{}
	mut := &outboxTestMutator{}
	subs := &outboxTestSubtitles{}
	h, err := newClipRenderDriveDeliveryHandler(pub, mut, subs, zap.NewNop())
	if err != nil {
		t.Fatalf("newClipRenderDriveDeliveryHandler: %v", err)
	}
	claim := &pgmedia.OutboxClaim{Event: pgmedia.OutboxEvent{EventKey: "key-3", PayloadJSON: string(raw)}}
	if err := h.Handle(context.Background(), claim); err == nil {
		t.Fatal("an incomplete sidecar block must be rejected")
	}
	if len(subs.upserted) != 0 {
		t.Fatalf("subtitle artifact upserts = %d, want 0 for a rejected payload", len(subs.upserted))
	}
}
