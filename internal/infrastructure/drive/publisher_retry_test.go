package drive

// retry surface tests for the drive publisher.

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
)

func TestYouTubeClipPath_IsDeterministic(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "NBA News",
		Subject: "abc123",
	}

	first, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	second, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, []string{"NBA News", "abc123"}, first)
}

func TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	first, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		retry, err := delivery.YouTubeClipPath(req)
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

	segs, err := delivery.YouTubeClipPath(req)
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

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err, "YouTubeClipPath must succeed with Group=Boxe and Subject='Pacquiao vs Broner'")

	// SafeFolderName preserves alphanum, spaces, and hyphens verbatim —
	// "Boxe" and "Pacquiao vs Broner" pass through unchanged.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, segs,
		"YouTubeClipPath must return canonical [{group},{subject}] segments — SafeFolderName preserves spaces and hyphens")
}

func TestYouTubeClipPath_WithRootFolderOverride_UsesSingleLeaf(t *testing.T) {
	req := delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Subject:            "qQIsvIOQS8U",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"qQIsvIOQS8U"}, segs)

	req = delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Group:              "boxing-channels",
	}
	segs, err = delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"boxing-channels"}, segs)
}
