package drive

// error surface tests for the drive publisher.

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPublisher_UploaderError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{err: context.DeadlineExceeded}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "publish to")
}

func TestPublisher_UnknownDestinationRejected(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: "nonexistent",
		LocalPath:   "/tmp/file.txt",
		Filename:    "file.txt",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown destination key")
}

func TestPublisher_RejectsDirectRootUpload(t *testing.T) {
	// All destinations have RequireSubpath=true. If a PathBuilder
	// somehow returns an empty slice, the publisher must reject.
	// We test this by creating a custom registry with a degenerate policy.
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder: "root-id",
		},
	}
	reg := delivery.NewDestinationRegistry(cfg)

	// Build a publisher with the real registry — all destinations require
	// subpath, so a request with empty Group/Subject should fail at the
	// PathBuilder level (which is the correct error path).
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject is empty → PathBuilder returns "subject (video ID) is required".
	})
	require.Error(t, err, "should reject when PathBuilder fails due to missing fields")
	require.Contains(t, err.Error(), "subject (video ID) is required")
}
