package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
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
	in := cliprender.RenderPublishInput{
		RunID:         "run-pub-1",
		SourceAssetID: "source-asset-001",
		SourceTitle:   title,
		OutputPath:    videoPath,
		Outcome: &cliprender.RenderOutcome{
			OutputPath:  videoPath,
			SizeBytes:   1,
			DurationSec: 3,
		},
		DriveFolderID: folderID,
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

// TestClipRenderPublisher_BurnMode_NeverUploadsAss pins the canonical rule:
// burned subtitles are baked into the video frames and the .ass artifact is a
// temporary render-internal file — the publisher uploads ONLY the MP4 and the
// publication carries no sidecar identity.
func TestClipRenderPublisher_BurnMode_NeverUploadsAss(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p, err := NewClipRenderPublisher(drive, committer, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPublisher() error = %v", err)
	}
	video := writeFakeVideo(t)
	title := "Kelly Clarkson Loses It After Spotting Meryl Streep"

	res, err := p.Publish(context.Background(), publishInput(video, title, cliprender.SubtitlesModeBurn, "leaf-123"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	reqs := drive.publishRequests()
	if len(reqs) != 1 {
		t.Fatalf("Drive uploads = %d, want 1 (burn mode must never upload the .ass sidecar)", len(reqs))
	}
	if reqs[0].Filename != title+".mp4" {
		t.Errorf("Drive filename = %q, want the human title + .mp4", reqs[0].Filename)
	}
	if reqs[0].DestinationFolderID != "leaf-123" {
		t.Errorf("destination folder = %q, want the resolved leaf verbatim", reqs[0].DestinationFolderID)
	}
	if len(reqs[0].DestinationSubpath) != 0 {
		t.Errorf("publisher must never create folders (subpath = %v)", reqs[0].DestinationSubpath)
	}
	if res.SidecarFileID != "" || res.SidecarLink != "" {
		t.Errorf("burn-mode publication must carry no sidecar identity, got file=%q link=%q", res.SidecarFileID, res.SidecarLink)
	}
	commits := committer.commitRequests()
	if len(commits) != 1 {
		t.Fatalf("asset commits = %d, want 1", len(commits))
	}
	if commits[0].Name != title+".mp4" || commits[0].Filename != title+".mp4" {
		t.Errorf("commit name/filename = %q/%q, want the human Drive filename", commits[0].Name, commits[0].Filename)
	}
	if commits[0].Title != title {
		t.Errorf("commit title = %q, want the source title", commits[0].Title)
	}
	if commits[0].FolderID != "leaf-123" {
		t.Errorf("commit folder = %q, want leaf-123", commits[0].FolderID)
	}
}

// TestClipRenderPublisher_SidecarMode_UploadsAssOnlyWhenExplicit pins the
// explicit opt-in: subtitles.mode=sidecar IS the caller's sidecar-export
// request — the .ass is uploaded next to the MP4 with the same human base
// name, and the publication carries the sidecar Drive identity.
func TestClipRenderPublisher_SidecarMode_UploadsAssOnlyWhenExplicit(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p, err := NewClipRenderPublisher(drive, committer, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPublisher() error = %v", err)
	}
	video := writeFakeVideo(t)
	title := "She Can't Stop Laughing During This Late-Night Interview"
	safeTitle := textutil.SanitizeFilename(title) // apostrophe stripped by the canonical filename sanitizer
	if safeTitle == title {
		t.Fatalf("test title must exercise sanitisation, got %q", title)
	}

	res, err := p.Publish(context.Background(), publishInput(video, title, cliprender.SubtitlesModeSidecar, "leaf-456"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	reqs := drive.publishRequests()
	if len(reqs) != 2 {
		t.Fatalf("Drive uploads = %d, want 2 (video + explicit sidecar)", len(reqs))
	}
	var videoName, sidecarName string
	for _, r := range reqs {
		if r.DestinationFolderID != "leaf-456" {
			t.Errorf("upload %q destination = %q, want the resolved leaf verbatim", r.Filename, r.DestinationFolderID)
		}
		switch {
		case strings.HasSuffix(r.Filename, ".mp4"):
			videoName = r.Filename
		case strings.HasSuffix(r.Filename, ".ass"):
			sidecarName = r.Filename
		}
	}
	if videoName != safeTitle+".mp4" {
		t.Errorf("video filename = %q, want the sanitized human title + .mp4", videoName)
	}
	if sidecarName != safeTitle+".ass" {
		t.Errorf("sidecar filename = %q, want the sanitized human title + .ass", sidecarName)
	}
	if res.SidecarFileID == "" || res.SidecarLink == "" {
		t.Errorf("sidecar-mode publication must carry the sidecar Drive identity, got file=%q link=%q", res.SidecarFileID, res.SidecarLink)
	}
	if len(committer.commitRequests()) != 1 {
		t.Fatalf("asset commits = %d, want 1 (the .ass sidecar is a Drive artifact, not a separate media asset)", len(committer.commitRequests()))
	}
}

// TestClipRenderPublisher_NoSubtitles_UploadsOnlyMP4 verifies a render
// without subtitles publishes exactly one artifact.
func TestClipRenderPublisher_NoSubtitles_UploadsOnlyMP4(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p, err := NewClipRenderPublisher(drive, committer, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPublisher() error = %v", err)
	}
	video := writeFakeVideo(t)

	res, err := p.Publish(context.Background(), publishInput(video, "Plain Clip", "", "leaf-789"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if len(drive.publishRequests()) != 1 {
		t.Fatalf("Drive uploads = %d, want 1", len(drive.publishRequests()))
	}
	if res.SidecarFileID != "" {
		t.Errorf("no-subtitle publication must carry no sidecar identity, got %q", res.SidecarFileID)
	}
}

// TestClipRenderPublisher_WithoutTitle_UsesAssetIDFilename pins the machine
// fallback: when the source asset has no human title, the Drive filename is
// the deterministic content-addressed asset ID (cliprender_<hash-prefix>),
// keeping the human/machine naming split intact.
func TestClipRenderPublisher_WithoutTitle_UsesAssetIDFilename(t *testing.T) {
	drive := &fakeDeliveryPublisher{}
	committer := &fakeAssetCommitter{}
	p, err := NewClipRenderPublisher(drive, committer, zap.NewNop())
	if err != nil {
		t.Fatalf("NewClipRenderPublisher() error = %v", err)
	}
	video := writeFakeVideo(t)

	res, err := p.Publish(context.Background(), publishInput(video, "", cliprender.SubtitlesModeBurn, "leaf-000"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	reqs := drive.publishRequests()
	if len(reqs) != 1 {
		t.Fatalf("Drive uploads = %d, want 1", len(reqs))
	}
	name := reqs[0].Filename
	if !strings.HasPrefix(name, "cliprender_") || !strings.HasSuffix(name, ".mp4") {
		t.Errorf("fallback filename = %q, want cliprender_<hash>.mp4", name)
	}
	if res.AssetID == "" || !strings.HasPrefix(res.AssetID, "cliprender_") {
		t.Errorf("asset id = %q, want cliprender_<hash-prefix>", res.AssetID)
	}
}
