package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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

type recordingExtractor struct {
	lastReq *youtubetypes.ExtractRequest
}

func (r *recordingExtractor) Extract(_ context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	r.lastReq = req
	return &youtubetypes.ExtractResponse{
		OK:              true,
		SourceURL:       req.URL,
		VideoID:         "vdC5GXxS-qU",
		DriveFolderID:   "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ",
		DriveFolderPath: "Manny Pacquiao vs Adrien Broner",
		Stats: &youtubetypes.ExtractStats{
			Requested: 3,
			Processed: 0,
			Skipped:   3,
			Failed:    0,
		},
	}, nil
}

func TestHandleJob_ThreeSegmentPayload_ClassificationSuccess(t *testing.T) {
	extractor := &recordingExtractor{}
	h := NewJobHandler(extractor, zap.NewNop())

	payload := map[string]any{
		"url": "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"segments": []map[string]any{
			{"start": "01:05", "end": "01:20", "name": "scene-0"},
			{"start": "02:26", "end": "02:35", "name": "scene-1"},
			{"start": "03:13", "end": "03:25", "name": "scene-2"},
		},
		"destination": map[string]any{
			"folder_id":        "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ",
			"group":            "Manny Pacquiao vs Adrien Broner",
			"subfolder_name":   "Manny Pacquiao vs Adrien Broner",
			"create_subfolder": true,
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	res, err := h.HandleJob(context.Background(), &job.Job{ID: "job-1", Payload: raw}, &appjobs.JobTools{})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, extractor.lastReq)
	require.Len(t, extractor.lastReq.Segments, 3)
	require.Equal(t, "https://www.youtube.com/watch?v=vdC5GXxS-qU", extractor.lastReq.URL)
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", extractor.lastReq.Destination.Group)
	require.Equal(t, "YouTube clip extraction completed", res["message"])
	require.Equal(t, "vdC5GXxS-qU", res["video_id"])
	require.Equal(t, 3, res["stats"].(*youtubetypes.ExtractStats).Requested)
}
