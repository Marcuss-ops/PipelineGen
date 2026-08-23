package drive

// folder surface tests for the drive publisher.

import (
	"context"
	"testing"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPublisher_EmptyRootFolderRejected(t *testing.T) {
	// Create a registry where one destination has an empty root.
	cfg := &config.Config{
		Drive: config.DriveConfig{
			// All roots empty → ClipsFolder() returns ""
			MediaRootFolder: "",
		},
	}
	reg := platformdelivery.NewDestinationRegistry(cfg)
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
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
	require.Contains(t, err.Error(), "no configured root folder")
}

func TestPublisher_FolderManagerError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{err: context.DeadlineExceeded}
	files := &fakeFileUploader{}
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
	require.Contains(t, err.Error(), "resolve drive path")
}

func TestPublisher_PublishYouTubeClip_CategoryBoxe_EnsureFolderSegments_DOD_10_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "boxe-pacquiao-broner-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/pacquiao_broner_clip.mp4",
		Filename:    "pacquiao_broner_clip.mp4",
		Description: "Pacquiao vs Broner highlights",
		AssetID:     "yt-pacquiao-broner-001",
		Group:       "Boxe",
		Subject:     "Pacquiao vs Broner",
		Category:    "Boxe",
		// No ParentFolderID — pure semantic routing via registry.
	})
	require.NoError(t, err)

	// (1) PublishResult carries the resolved folder and path segments.
	require.Equal(t, "boxe-pacquiao-broner-folder-id", result.FolderID,
		"PublishResult.FolderID must equal the leaf folder returned by EnsureFolder")
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, result.PathSegments,
		"PublishResult.PathSegments must be the canonical [{group},{subject}] structure")
	require.Equal(t, "Boxe/Pacquiao vs Broner", result.FolderPath,
		"PublishResult.FolderPath must be the slash-joined display form of PathSegments")
	require.Equal(t, delivery.DestinationYouTubeClip, result.Destination,
		"PublishResult.Destination must echo back the requested DestinationKey")

	// (2) DoD 10 integration contract: the fake FolderManager recorded exactly
	//     one EnsureFolder call with the CORRECT parent and segments.
	require.Len(t, folders.ensureCalls, 1,
		"exactly one EnsureFolder call must be made (single Publish call)")
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent,
		"EnsureFolder parent must be the registry root folder ID 'clips-root'")
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, folders.ensureCalls[0].segments,
		"EnsureFolder segments must be the canonical [{group},{subject}] structure after PathBuilder + SafeFolderName")

	// (3) The uploader was called with the resolved folder.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "boxe-pacquiao-broner-folder-id", files.uploadCalls[0].folderID,
		"upload must land in the folder returned by EnsureFolder")
	require.Equal(t, "pacquiao_broner_clip.mp4", files.uploadCalls[0].filename,
		"upload filename must match the requested filename verbatim")
	require.Equal(t, "Pacquiao vs Broner highlights", files.uploadCalls[0].description,
		"upload description must match the requested description verbatim")
}

func TestPublisher_PublishYouTubeClip_NoFolderOverride_PureSemanticRouting_DOD_10_2(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "semantic-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	// Pure semantic routing: no ParentFolderID, no FolderID — just
	// Group + Subject + Category. The Publisher resolves everything.
	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/semantic_clip.mp4",
		Filename:    "semantic_clip.mp4",
		Group:       "Boxe",
		Subject:     "Pacquiao vs Broner",
		Category:    "Boxe",
		// ParentFolderID intentionally omitted — zero value.
	})
	require.NoError(t, err)

	// (1) EnsureFolder was called with registry root, NOT an override.
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent,
		"when ParentFolderID is empty, EnsureFolder parent MUST be the registry root 'clips-root'")

	// (2) Result carries the canonical path.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, result.PathSegments)
	require.Equal(t, "semantic-folder-id", result.FolderID)

	// (3) Upload landed in the semantic folder.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "semantic-folder-id", files.uploadCalls[0].folderID)
}
