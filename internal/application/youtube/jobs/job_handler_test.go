package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

func TestBuildResultMapIncludesDriveFolderPath(t *testing.T) {
	h := &JobHandler{}
	resp := &youtubetypes.ExtractResponse{
		OK:              true,
		SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		VideoID:         "vdC5GXxS-qU",
		DriveFolderID:   "folder-id",
		DriveFolderPath: "Pacquiao_Vs_Broner/vdC5GXxS-qU",
	}

	result := h.buildResultMap(resp, "done")

	require.Equal(t, "folder-id", result["drive_folder_id"])
	require.Equal(t, "Pacquiao_Vs_Broner/vdC5GXxS-qU", result["drive_folder_path"])
}
