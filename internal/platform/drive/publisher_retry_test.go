package drive

// retry surface tests for the drive publisher.

import (
	"context"
	"testing"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestYouTubeClipPath_IsDeterministic(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "NBA News",
		Subject: "abc123",
	}

	first, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)

	second, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, []string{"NBA News", "abc123"}, first)
}

func TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	first, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		retry, err := platformdelivery.YouTubeClipPath(req)
		require.NoError(t, err, "retry %d must not fail", i)
		require.Equal(t, first, retry,
			"retry %d: YouTubeClipPath must be byte-stable idempotent — same inputs must produce same segments", i)
	}
}

func TestYouTubeClipPath_PathBuilderSanitisesSpecialCharacters_DOD_9_3(t *testing.T) {
	// Subject with a YouTube-style video ID containing special chars
	// that SafeFolderName would replace (slashes → spaces/hyphens).
	req := delivery.PublishRequest{
		Group:   "NBA / Highlights",
		Subject: "game:7/OT",
	}

	segs, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)

	// SafeFolderName replaces / and : with safe alternatives (spaces/hyphens).
	require.Equal(t, 2, len(segs), "must produce exactly 2 segments [{group},{subject}]")
	// Positive assertions: SafeFolderName replaces non-alphanum (except -/_) with _.
	require.Equal(t, "NBA _ Highlights", segs[0],
		"SafeFolderName must replace / with _ in group segment")
	require.Equal(t, "game_7_OT", segs[1],
		"SafeFolderName must replace / with _ and : with _ in subject segment")
}

func TestYouTubeClipPath_CategoryBoxeSubjectPacquiaoVsBroner_DOD_9_1(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	segs, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err, "YouTubeClipPath must succeed with Group=Boxe and Subject='Pacquiao vs Broner'")

	// SafeFolderName preserves alphanum, spaces, and hyphens verbatim —
	// "Boxe" and "Pacquiao vs Broner" pass through unchanged.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, segs,
		"YouTubeClipPath must return canonical [{group},{subject}] segments — SafeFolderName preserves spaces and hyphens")
}

func TestPublisher_ExplicitDestinationFolderBypassesForSidecars(t *testing.T) {
	// Sidecar contract (voiceover, clip metadata, texttracks — the callers
	// that pass a pre-resolved DestinationFolderID): an explicit
	// DestinationFolderID is the complete leaf destination and MUST bypass
	// the registry AND the path builder even with an explicit conflict
	// policy. This is distinct from the YouTube clip path, which threads
	// ParentFolderID and always nests (see
	// TestPublisher_YouTubeClipWithRootOverride_EnsuresNestedSegments).
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:         delivery.DestinationKey("unknown-destination"),
		DestinationFolderID: "resolved-voiceover-folder",
		LocalPath:           "/tmp/voiceover.mp3",
		Filename:            "voiceover.mp3",
		ConflictPolicy:      delivery.ConflictOverwrite,
	})
	require.NoError(t, err, "an already-resolved explicit folder must not require registry/path-builder resolution")
	require.Equal(t, "resolved-voiceover-folder", result.FolderID)
	require.Empty(t, result.PathSegments)
	require.Len(t, folders.ensureCalls, 0, "explicit destination must not create a configured subpath")
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "resolved-voiceover-folder", files.uploadCalls[0].folderID)
}

func TestPublisher_OverlaySubpathIsCreatedBelowResolvedArtifactFolder(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "overlay-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:         delivery.DestinationYouTubeClip,
		DestinationFolderID: "artifact-folder-847",
		DestinationSubpath:  []string{"overlay"},
		LocalPath:           "/tmp/overlay_001.mov",
		Filename:            "overlay_001.mov",
		ConflictPolicy:      delivery.ConflictSkip,
		IdempotencyKey:      "overlay-idem-001",
		ContentHash:         "overlay-sha-001",
	})
	require.NoError(t, err)
	require.Equal(t, "overlay-folder-id", result.FolderID)
	require.Equal(t, []string{"overlay"}, result.PathSegments)
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "artifact-folder-847", folders.ensureCalls[0].parent)
	require.Equal(t, []string{"overlay"}, folders.ensureCalls[0].segments)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "overlay-folder-id", files.uploadCalls[0].folderID)
}

func TestPublisher_YouTubeClipWithRootOverride_EnsuresNestedSegments(t *testing.T) {
	// Production contract (adapter decision, ba84a9eaf): the
	// YouTubePublisherDriveAdapter threads the resolved folder into
	// ParentFolderID — NOT DestinationFolderID. The publisher therefore
	// re-runs YouTubeClipPath and creates the per-video nested subfolder
	// inside the resolved root: {folder}/{group}/{video_id}.
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		ParentFolderID: "resolved-actor-folder",
		Group:          "Matt Damon",
		Subject:        "e35PVH3ksFA",
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip.mp4",
		ConflictPolicy: delivery.ConflictSkip,
	})
	require.NoError(t, err)
	require.Len(t, folders.ensureCalls, 1, "nested per-video folder must be ensured inside the resolved root")
	require.Equal(t, "resolved-actor-folder", folders.ensureCalls[0].parent)
	require.Equal(t, []string{"Matt Damon", "e35PVH3ksFA"}, folders.ensureCalls[0].segments)
	require.Equal(t, []string{"Matt Damon", "e35PVH3ksFA"}, result.PathSegments)
	require.Equal(t, "fake-folder-id", result.FolderID)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "fake-folder-id", files.uploadCalls[0].folderID)
}

func TestPublisher_ExplicitDestinationFolderWithUnsetPolicyBypassesRegistry(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:         delivery.DestinationKey("unknown-destination"),
		DestinationFolderID: "resolved-voiceover-folder",
		LocalPath:           "/tmp/voiceover.mp3",
		Filename:            "voiceover.mp3",
		// ConflictPolicyUnset must not force a registry lookup when
		// the destination folder was already resolved by the plan.
	})
	require.NoError(t, err)
	require.Equal(t, "resolved-voiceover-folder", result.FolderID)
	require.Len(t, folders.ensureCalls, 0)
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy,
		"pre-resolved immutable voiceover destinations use the conservative skip default")
}

func TestYouTubeClipPath_ExplicitFolderViaRootOverride_StillBuildsNestedSegments(t *testing.T) {
	// Production contract (adapter decision, ba84a9eaf): the adapter passes
	// the resolved folder as ParentFolderID, so YouTubeClipPath is ALWAYS
	// re-run and the per-video subfolder is created. An explicit actor folder
	// is the ROOT of the nested path, never a verbatim leaf.
	//
	// With a group present the path is {group}/{video_id} — no fallback.
	req := delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		ParentFolderID: "matt-damon-folder",
		Group:          "Matt Damon",
		Category:       "actor_clip",
		Subject:        "e35PVH3ksFA",
	}

	segments, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"Matt Damon", "e35PVH3ksFA"}, segments,
		"YouTubeClipPath must build {group}/{video_id} under a ParentFolderID")
	require.NotContains(t, segments, "youtube_uncategorized")
}

func TestYouTubeClipPath_ExplicitFolderViaRootOverride_WithoutGroupFallsBackToUncategorized(t *testing.T) {
	// The uVoMqnwEdBQ regression (2026-08-06): a clip whose request had a
	// folder but no group/category lands in {folder}/youtube_uncategorized/{video}
	// — the fallback chain (Group → Category → "youtube_uncategorized") applies
	// under a ParentFolderID just as it does without one.
	req := delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		ParentFolderID: "1omaKrmSHurA9y", // "Tom Holland"
		Subject:        "uVoMqnwEdBQ",
	}

	segments, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"youtube_uncategorized", "uVoMqnwEdBQ"}, segments,
		"YouTubeClipPath must fall back to youtube_uncategorized when group/category are empty")
}

func TestYouTubeClipPath_WithDestinationFolderID_UsesFolderVerbatim(t *testing.T) {
	req := delivery.PublishRequest{
		DestinationFolderID: "explicit-root",
		Subject:             "qQIsvIOQS8U",
	}

	segs, err := platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Empty(t, segs)

	req = delivery.PublishRequest{
		DestinationFolderID: "explicit-root",
		Group:               "boxing-channels",
	}
	segs, err = platformdelivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Empty(t, segs)
}
