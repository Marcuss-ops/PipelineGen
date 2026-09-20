// extraction_fanout_command_test.go — pins the Command envelope the
// fan-out hands to the canonical per-segment pipeline.
//
// Sept 2026 cleanup: SubtitleFolderID used to be written twice (empty in
// the builder, then overwritten by the fan-out goroutine) and
// SubtitleFolderPath was a dead field fed by a helper that always
// returned "". The builder is now the SOLE writer of the resolved folder
// id, so these two properties are pinned together.
package usecase

import (
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

func TestBuildSegmentCommand_WiresResolvedSubtitleFolderID(t *testing.T) {
	req := &youtubetypes.ExtractRequest{URL: "https://www.youtube.com/watch?v=abc12345678"}

	cmd := buildSegmentCommand(
		req,
		youtubetypes.Segment{Start: "00:00", End: "00:05", Name: "probe-0"},
		0,
		"abc12345678",
		"/tmp/out",
		"drive-folder-id",
		"Group/abc12345678",
		"subtitle-folder-id",
		true,
	)

	require.Equal(t, "subtitle-folder-id", cmd.SubtitleFolderID,
		"the resolved subtitle folder id must arrive via the builder, not a second assignment")
	require.Equal(t, "abc12345678", cmd.VideoID)
	require.Equal(t, "/tmp/out", cmd.OutDir)
	require.Equal(t, "drive-folder-id", cmd.DriveFolderID)
	require.Equal(t, "Group/abc12345678", cmd.DriveFolderPath)
	require.NotNil(t, cmd.KeepAudio)
	require.True(t, *cmd.KeepAudio)
}
