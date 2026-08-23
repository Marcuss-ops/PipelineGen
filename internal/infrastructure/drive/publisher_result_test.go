package drive

// result surface tests for the drive publisher.

import (
	"context"
	"testing"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPublisher_PublishEnrichesPublishResult_F1_5_P0_9(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		putAction:   PutActionUpdated, // non-default to observe translation
		md5Checksum: "abc123def456",   // non-empty to observe propagation
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_abc.mp4",
		Group:       "NBA News",
		Subject:     "video-xyz",
	})
	require.NoError(t, err)

	// (1) DownloadLink propagated verbatim from PutFileResult —
	//     verifies the no-reconstruction contract.
	require.Equal(t, "https://drive.google.com/uc?id=fake-file-id", result.DownloadLink,
		"Publish must propagate PutFileResult.DownloadLink verbatim — consumers MUST never reconstruct via uc?id=")

	// (2) MD5Checksum propagated verbatim — closes the re-FindFileByName
	//     surface that pre-P0-#9 callers used to recover the hash.
	require.Equal(t, "abc123def456", result.MD5Checksum,
		"Publish must propagate PutFileResult.MD5Checksum verbatim")

	// (3) Action translated at the delivery/drive boundary via the
	//     SAME method that Publish calls (single source of truth).
	require.Equal(t, pub.actionFor(PutActionUpdated), result.Action,
		"Publish must translate drive.PutActionUpdated → delivery.PublishActionUpdated at the boundary")
	require.NotEqual(t, delivery.PublishActionUnknown, result.Action,
		"PublishActionUnknown must NOT silently swallow a real action")

	// (4) FolderPath = strings.Join(PathSegments, "/") — the derived
	//     single-string view that consumers downstream want.
	require.Equal(t, "NBA News/video-xyz", result.FolderPath,
		"Publish must compute FolderPath as strings.Join(PathSegments, \"/\")")

	// (5) PathSegments remains authoritative: FolderPath and
	//     PathSegments coexist; PathSegments preserves order and
	//     is the canonical ordered view (FolderPath is derived).
	require.Equal(t, []string{"NBA News", "video-xyz"}, result.PathSegments,
		"PathSegments remains the authoritative ordered view; FolderPath is the derived display string")
}

func TestPublisher_TranslatePutAction_Table(t *testing.T) {
	reg := testRegistry()
	pub, err := NewPublisher(reg, &fakeFolderManager{}, &fakeFileUploader{}, zap.NewNop())
	require.NoError(t, err)

	cases := []struct {
		name     string
		input    PutAction
		expected delivery.PublishAction
	}{
		{"created", PutActionCreated, delivery.PublishActionCreated},
		{"updated", PutActionUpdated, delivery.PublishActionUpdated},
		{"skipped", PutActionSkipped, delivery.PublishActionSkipped},
		{"renamed", PutActionRenamed, delivery.PublishActionRenamed},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.expected, pub.actionFor(c.input),
				"drive.PutAction%q must translate to delivery.PublishAction%q at the boundary", c.input, c.expected)
		})
	}
}
