package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// fakeAssetCommitter is a persistence.AssetCommitter double capturing the
// CommitAsset requests. The embedded nil interface satisfies the full
// interface while keeping the fake single-purpose: only CommitAsset is
// exercised by the ClipRenderPublisher.
type fakeAssetCommitter struct {
	persistence.AssetCommitter
	mu       sync.Mutex
	requests []persistence.AssetCommitRequest
}

func (f *fakeAssetCommitter) CommitAsset(_ context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return persistence.CommittedAsset{}, nil
}

func (f *fakeAssetCommitter) commitRequests() []persistence.AssetCommitRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]persistence.AssetCommitRequest(nil), f.requests...)
}

// newAsyncPublisher builds a publisher with the durable staging root every
// production render now needs. clip.render Drive delivery is unconditionally
// asynchronous, so there is no mode to select.
func newAsyncPublisher(t *testing.T, drive *fakeDeliveryPublisher, committer *fakeAssetCommitter) *ClipRenderPublisher {
	t.Helper()
	p, err := NewClipRenderPublisher(drive, committer, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPublisher() error = %v", err)
	}
	p.SetAsyncDriveStagingRoot(filepath.Join(t.TempDir(), "cliprender-staging"))
	return p
}

// deliveryIntent decodes the single durable Drive-delivery intent the publisher
// committed. Its presence (and the ABSENCE of a synchronous Drive upload) is
// the whole contract now.
func deliveryIntent(t *testing.T, committer *fakeAssetCommitter) (cliprender.ClipRenderDriveDeliveryRequest, persistence.AssetCommitRequest) {
	t.Helper()
	commits := committer.commitRequests()
	if len(commits) != 1 {
		t.Fatalf("asset commits = %d, want 1", len(commits))
	}
	events := commits[0].AdditionalOutboxEvents
	if len(events) != 1 {
		t.Fatalf("additional outbox events = %d, want 1", len(events))
	}
	if events[0].EventType != cliprender.EventClipRenderDriveDeliveryRequested {
		t.Fatalf("outbox event type = %q, want %q", events[0].EventType, cliprender.EventClipRenderDriveDeliveryRequested)
	}
	var payload cliprender.ClipRenderDriveDeliveryRequest
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode delivery payload: %v", err)
	}
	return payload, commits[0]
}

func writeFakeVideo(t *testing.T) string {
	t.Helper()
	content := []byte("fake-rendered-mp4-bytes-for-content-digest")
	path := filepath.Join(t.TempDir(), "rendered-clip.mp4")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fake video: %v", err)
	}
	return path
}

func publishInput(videoPath, title, mode, folderID string) cliprender.RenderPublishInput {
	// The publisher adopts the caller-certified digest and never re-reads the
	// artifact, so the fixture certifies the exact bytes it wrote.
	contentHash, size, _ := digest.SHA256File(videoPath)
	in := cliprender.RenderPublishInput{
		RunID:         "run-pub-1",
		SourceAssetID: "source-asset-001",
		SourceTitle:   title,
		OutputPath:    videoPath,
		Outcome: &cliprender.RenderOutcome{
			OutputPath:  videoPath,
			SizeBytes:   size,
			SHA256:      contentHash,
			DurationSec: 3,
		},
		CertifiedSHA256:    contentHash,
		CertifiedSizeBytes: size,
		DriveFolderID:      folderID,
	}
	if mode != "" {
		in.Subtitles = &cliprender.SubtitleArtifact{
			LocalPath: filepath.Join(filepath.Dir(videoPath), "subtitles.ass"),
			SHA256:    strings.Repeat("ab", 32),
			Mode:      mode,
		}
	}
	return in
}

// TestClipRenderPublisher_LocatorOnly_StreamsFromObjectStore pins the
// locator-first contract: when the render outcome carries a durable object-store
// locator and no local file, the publisher stages NOTHING locally and emits a
// delivery intent the outbox streams from the URL.
func TestClipRenderPublisher_LocatorOnly_StreamsFromObjectStore(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)

	contentHash := strings.Repeat("ab", 32)
	artifactURL := "http://objectstore:9000/objects/" + contentHash
	in := cliprender.RenderPublishInput{
		RunID: "run-locator", SourceAssetID: "source-asset-001", SourceTitle: "Locator Clip",
		ArtifactStorageKey:  contentHash,
		ArtifactURL:         artifactURL,
		ArtifactContentType: "video/mp4",
		Outcome: &cliprender.RenderOutcome{
			SizeBytes: 4096, SHA256: contentHash, DurationSec: 3,
			StorageKey: contentHash, ArtifactURL: artifactURL, ContentType: "video/mp4",
		},
		CertifiedSHA256: contentHash, CertifiedSizeBytes: 4096,
		DriveFolderID: "folder-locator",
	}
	res, err := p.Publish(context.Background(), in)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if res == nil || !res.DrivePending || res.SizeBytes != 4096 {
		t.Fatalf("result = %+v, want DrivePending with size 4096", res)
	}
	payload, commit := deliveryIntent(t, committer)
	if payload.LocalPath != "" {
		t.Errorf("intent LocalPath = %q, want empty (no local staging)", payload.LocalPath)
	}
	if payload.ArtifactURL != artifactURL || payload.StorageKey != contentHash || payload.ContentType != "video/mp4" {
		t.Errorf("intent locator = %+v, want the certified object-store locator", payload)
	}
	if payload.Filename != "Locator Clip.mp4" {
		t.Errorf("intent filename = %q, want the source title + .mp4 (extension from content type)", payload.Filename)
	}
	if commit.LocalPath != "" {
		t.Errorf("committed LocalPath = %q, want empty", commit.LocalPath)
	}
}

// TestClipRenderPublisher_RequiresOutputOrLocator pins the fail-closed rule:
// neither a local path nor a locator is a typed error.
func TestClipRenderPublisher_RequiresOutputOrLocator(t *testing.T) {
	p := newAsyncPublisher(t, &fakeDeliveryPublisher{}, &fakeAssetCommitter{})
	_, err := p.Publish(context.Background(), cliprender.RenderPublishInput{
		RunID: "run-missing", DriveFolderID: "folder-1",
		Outcome:            &cliprender.RenderOutcome{SizeBytes: 10, SHA256: strings.Repeat("cd", 32)},
		CertifiedSHA256:    strings.Repeat("cd", 32),
		CertifiedSizeBytes: 10,
	})
	if err == nil {
		t.Fatal("Publish must fail closed without an output path or artifact locator")
	}
}

// TestClipRenderPublisher_BurnMode_NeverUploadsAss pins the canonical rule:
// burned subtitles are baked into the video frames and the .ass artifact is a
// temporary render-internal file — the delivery intent carries ONLY the MP4.
func TestClipRenderPublisher_BurnMode_NeverUploadsAss(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)
	title := "Kelly Clarkson Loses It After Spotting Meryl Streep"

	res, err := p.Publish(context.Background(), publishInput(video, title, cliprender.SubtitlesModeBurn, "leaf-123"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := len(drive.publishRequests()); got != 0 {
		t.Fatalf("Drive uploads = %d, want 0: the external round-trip must never sit on the render path", got)
	}
	payload, commit := deliveryIntent(t, committer)
	if payload.Sidecar != nil {
		t.Errorf("burn mode must never carry a sidecar bundle, got %+v", payload.Sidecar)
	}
	if payload.Filename != title+".mp4" {
		t.Errorf("delivery filename = %q, want the human title + .mp4", payload.Filename)
	}
	if payload.FolderID != "leaf-123" {
		t.Errorf("delivery folder = %q, want the resolved leaf verbatim", payload.FolderID)
	}
	if !res.DrivePending {
		t.Fatal("async publication must report DrivePending")
	}
	if res.SidecarFileID != "" || res.SidecarLink != "" {
		t.Errorf("burn-mode publication must carry no sidecar identity, got file=%q link=%q", res.SidecarFileID, res.SidecarLink)
	}
	if commit.Name != title+".mp4" || commit.Filename != title+".mp4" {
		t.Errorf("commit name/filename = %q/%q, want the human Drive filename", commit.Name, commit.Filename)
	}
	if commit.FolderID != "leaf-123" {
		t.Errorf("commit folder = %q, want leaf-123", commit.FolderID)
	}
}

// TestClipRenderPublisher_FailsClosedWithoutCertifiedDigest pins item 8: the
// publisher adopts the digest the producing boundary already certified and
// never silently re-reads the artifact. An uncertified artifact is a typed
// error before any durable intent is committed.
func TestClipRenderPublisher_FailsClosedWithoutCertifiedDigest(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)

	in := publishInput(video, "Uncertified", cliprender.SubtitlesModeBurn, "leaf-uncertified")
	in.CertifiedSHA256 = ""
	in.CertifiedSizeBytes = 0
	if _, err := p.Publish(context.Background(), in); err == nil {
		t.Fatal("publication without a certified digest must fail closed")
	}
	if got := len(drive.publishRequests()); got != 0 {
		t.Fatalf("Drive uploads = %d, want 0 when the artifact is uncertified", got)
	}
	if got := len(committer.commitRequests()); got != 0 {
		t.Fatalf("asset commits = %d, want 0 when the artifact is uncertified", got)
	}
}

// TestClipRenderPublisher_StagesBeforeCommit pins the durable boundary: the
// artifact must move out of the job workspace before the outbox payload is
// committed, because the outbox consumer may run after the workspace cleanup.
func TestClipRenderPublisher_StagesBeforeCommit(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)

	res, err := p.Publish(context.Background(), publishInput(video, "Async Clip", cliprender.SubtitlesModeBurn, "leaf-async"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if !res.DrivePending {
		t.Fatal("async publication must report DrivePending")
	}
	if got := len(drive.publishRequests()); got != 0 {
		t.Fatalf("Drive uploads = %d, want 0 before outbox consumption", got)
	}
	payload, _ := deliveryIntent(t, committer)
	if payload.LocalPath == video {
		t.Fatal("delivery payload still points into the job workspace")
	}
	if _, err := os.Stat(payload.LocalPath); err != nil {
		t.Fatalf("staged artifact is unavailable: %v", err)
	}
	if _, err := os.Stat(video); !os.IsNotExist(err) {
		t.Fatalf("workspace artifact still exists after staging, err=%v", err)
	}
}

// TestClipRenderPublisher_CarriesSidecarBundle pins the sidecar bundle: a
// sidecar-mode request carries the ASS artifact INSIDE the same durable Drive
// intent as the video, so the render job completes without waiting for Drive in
// either subtitle mode.
func TestClipRenderPublisher_CarriesSidecarBundle(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)

	sidecarPath := filepath.Join(filepath.Dir(video), "subtitles.ass")
	if err := os.WriteFile(sidecarPath, []byte("[Script Info]\nTitle: bundle fixture\n"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	sidecarSHA, sidecarSize, err := digest.SHA256File(sidecarPath)
	if err != nil {
		t.Fatalf("hash sidecar: %v", err)
	}

	in := publishInput(video, "Bundle Clip", cliprender.SubtitlesModeSidecar, "leaf-bundle")
	in.Subtitles.LocalPath = sidecarPath
	in.Subtitles.SHA256 = sidecarSHA
	in.Transcript = &cliprender.TranscriptResult{Language: "en", TextSHA256: strings.Repeat("cd", 32)}

	res, err := p.Publish(context.Background(), in)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if !res.DrivePending {
		t.Fatal("async sidecar publication must report DrivePending")
	}
	if got := len(drive.publishRequests()); got != 0 {
		t.Fatalf("Drive uploads = %d, want 0 before outbox consumption", got)
	}
	payload, _ := deliveryIntent(t, committer)
	if payload.Sidecar == nil {
		t.Fatal("sidecar publication must carry the sidecar bundle in the delivery intent")
	}
	if payload.Sidecar.LocalPath == sidecarPath {
		t.Fatal("sidecar payload still points into the job workspace")
	}
	if _, err := os.Stat(payload.Sidecar.LocalPath); err != nil {
		t.Fatalf("staged sidecar is unavailable: %v", err)
	}
	if payload.Sidecar.SizeBytes != sidecarSize {
		t.Errorf("sidecar size = %d, want %d", payload.Sidecar.SizeBytes, sidecarSize)
	}
	if payload.Sidecar.SHA256 != sidecarSHA {
		t.Errorf("sidecar digest = %q, want %q", payload.Sidecar.SHA256, sidecarSHA)
	}
	if payload.Sidecar.LanguageCode != "en" {
		t.Errorf("sidecar language = %q, want en", payload.Sidecar.LanguageCode)
	}
	if !strings.HasSuffix(payload.Sidecar.Filename, ".ass") {
		t.Errorf("sidecar filename = %q, want *.ass", payload.Sidecar.Filename)
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Fatalf("workspace sidecar still exists after staging, err=%v", err)
	}
}

// TestClipRenderPublisher_SidecarMode_CarriesAssName pins the explicit opt-in:
// subtitles.mode=sidecar IS the caller's sidecar-export request, so the bundle
// carries the .ass under the same sanitized human base name as the MP4.
func TestClipRenderPublisher_SidecarMode_CarriesAssName(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)
	title := "She Can't Stop Laughing During This Late-Night Interview"
	safeTitle := textutil.SanitizeFilename(title) // apostrophe stripped by the canonical filename sanitizer
	if safeTitle == title {
		t.Fatalf("test title must exercise sanitisation, got %q", title)
	}

	sidecarPath := filepath.Join(filepath.Dir(video), "subtitles.ass")
	if err := os.WriteFile(sidecarPath, []byte("[Script Info]\nTitle: sidecar fixture\n"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	sidecarSHA, _, err := digest.SHA256File(sidecarPath)
	if err != nil {
		t.Fatalf("hash sidecar: %v", err)
	}
	in := publishInput(video, title, cliprender.SubtitlesModeSidecar, "leaf-456")
	in.Subtitles.LocalPath = sidecarPath
	in.Subtitles.SHA256 = sidecarSHA

	if _, err := p.Publish(context.Background(), in); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := len(drive.publishRequests()); got != 0 {
		t.Fatalf("Drive uploads = %d, want 0 before outbox consumption", got)
	}
	payload, _ := deliveryIntent(t, committer)
	if payload.Sidecar == nil {
		t.Fatal("sidecar mode must carry the sidecar bundle")
	}
	if payload.Filename != safeTitle+".mp4" {
		t.Errorf("video filename = %q, want the sanitized human title + .mp4", payload.Filename)
	}
	if payload.Sidecar.Filename != safeTitle+".ass" {
		t.Errorf("sidecar filename = %q, want the sanitized human title + .ass", payload.Sidecar.Filename)
	}
}

// TestClipRenderPublisher_NoSubtitles_UploadsOnlyMP4 verifies a render without
// subtitles carries exactly the video half of the bundle.
func TestClipRenderPublisher_NoSubtitles_UploadsOnlyMP4(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)

	res, err := p.Publish(context.Background(), publishInput(video, "Plain Clip", "", "leaf-789"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	payload, _ := deliveryIntent(t, committer)
	if payload.Sidecar != nil {
		t.Errorf("no-subtitle publication must carry no sidecar bundle, got %+v", payload.Sidecar)
	}
	if res.SidecarFileID != "" {
		t.Errorf("no-subtitle publication must carry no sidecar identity, got %q", res.SidecarFileID)
	}
}

// TestClipRenderPublisher_WithoutTitle_UsesAssetIDFilename pins the machine
// fallback: when the source asset has no human title, the Drive filename is the
// deterministic content-addressed asset ID (cliprender_<hash-prefix>), keeping
// the human/machine naming split intact.
func TestClipRenderPublisher_WithoutTitle_UsesAssetIDFilename(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p := newAsyncPublisher(t, drive, committer)
	video := writeFakeVideo(t)

	res, err := p.Publish(context.Background(), publishInput(video, "", cliprender.SubtitlesModeBurn, "leaf-000"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	payload, _ := deliveryIntent(t, committer)
	if !strings.HasPrefix(payload.Filename, "cliprender_") || !strings.HasSuffix(payload.Filename, ".mp4") {
		t.Errorf("fallback filename = %q, want cliprender_<hash>.mp4", payload.Filename)
	}
	if res.AssetID == "" || !strings.HasPrefix(res.AssetID, "cliprender_") {
		t.Errorf("asset id = %q, want cliprender_<hash-prefix>", res.AssetID)
	}
}
