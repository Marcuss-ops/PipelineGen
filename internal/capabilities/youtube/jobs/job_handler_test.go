package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
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

func TestHandleJob_ClassifiedFailure_PreservesEveryOriginalItemError(t *testing.T) {
	extractor := &multiFailingExtractor{}
	h := NewJobHandler(extractor, zap.NewNop())

	res, err := h.HandleJob(context.Background(), &job.Job{ID: "job-multi-fail"}, &appjobs.JobTools{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrExtractionTerminal)
	require.Equal(t, "terminal", res["failure_class"])

	details := failureDetailsFromError(t, err)
	require.Equal(t, float64(2), details["stats"].(map[string]any)["failed"])
	items := details["items"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "first original failure", items[0].(map[string]any)["error"])
	require.Equal(t, "second original failure", items[1].(map[string]any)["error"])
}

func TestHandleJob_ClassifiedFailure_PreservesOriginalCause(t *testing.T) {
	extractor := &failingExtractor{itemErr: "writer_failed: writer rejected locale-not-ready: clip locale not ready: asset=yt_z_k1UGy4-qU_3_15_v1 reason=missing READY translations"}
	h := NewJobHandler(extractor, zap.NewNop())

	payload := map[string]any{
		"url": "https://www.youtube.com/watch?v=z_k1UGy4-qU",
		"segments": []map[string]any{
			{"start": "00:00", "end": "00:05", "name": "probe-0"},
		},
		"destination": map[string]any{
			"folder_id":   "1KYyXMPF75XZHUM1QfMkwDiA7zUGgj_5T",
			"folder_path": "Jason Momoa",
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	res, err := h.HandleJob(context.Background(), &job.Job{ID: "job-fail-1", Payload: raw}, &appjobs.JobTools{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrExtractionTerminal)
	require.NotNil(t, res, "classified failure must NOT return a nil result")
	require.Equal(t, "terminal", res["failure_class"])
	require.Contains(t, err.Error(), "writer_failed")
	require.Contains(t, err.Error(), "missing READY translations")

	details := failureDetailsFromError(t, err)
	require.Equal(t, "terminal", details["failure_class"])
	require.Equal(t, "one or more segments failed", details["error"])
	require.Equal(t, float64(1), details["stats"].(map[string]any)["failed"])
	items := details["items"].([]any)
	require.Len(t, items, 1)
	require.Equal(t, extractor.itemErr, items[0].(map[string]any)["error"])
}

func TestHandleJob_NilExtractorResponse_IsStructuredTerminalFailure(t *testing.T) {
	h := NewJobHandler(nilResponseExtractor{}, zap.NewNop())

	res, err := h.HandleJob(context.Background(), &job.Job{ID: "job-nil-response"}, &appjobs.JobTools{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrExtractionTerminal)
	require.Equal(t, "terminal", res["failure_class"])

	details := failureDetailsFromError(t, err)
	require.Equal(t, "terminal", details["failure_class"])
	require.Equal(t, "extractor returned nil response", details["error"])
	require.Empty(t, details["items"])
}

func TestHandleJob_ClassifiedFailure_Retryable(t *testing.T) {
	extractor := &failingExtractor{itemErr: "ytdlp download failed: rate limit exceeded"}
	h := NewJobHandler(extractor, zap.NewNop())

	payload := map[string]any{
		"url": "https://www.youtube.com/watch?v=z_k1UGy4-qU",
		"segments": []map[string]any{
			{"start": "00:00", "end": "00:05", "name": "probe-0"},
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	res, err := h.HandleJob(context.Background(), &job.Job{ID: "job-retry-1", Payload: raw}, &appjobs.JobTools{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrExtractionRetryable)
	require.NotNil(t, res)
	require.Equal(t, "retryable", res["failure_class"])

	details := failureDetailsFromError(t, err)
	require.Equal(t, "retryable", details["failure_class"])
	require.Equal(t, extractor.itemErr, details["items"].([]any)[0].(map[string]any)["error"])
}

func failureDetailsFromError(t *testing.T, err error) map[string]any {
	t.Helper()
	const marker = "failure_details="
	_, payload, ok := strings.Cut(err.Error(), marker)
	require.True(t, ok, "classified error must include %q", marker)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &details))
	return details
}

type nilResponseExtractor struct{}

func (nilResponseExtractor) Extract(context.Context, *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	return nil, nil
}

type multiFailingExtractor struct{}

func (multiFailingExtractor) Extract(context.Context, *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	return &youtubetypes.ExtractResponse{
		Stats: &youtubetypes.ExtractStats{Requested: 2, Failed: 2},
		Items: []youtubetypes.ExtractItem{
			{Name: "first", Status: "failed", Error: "first original failure"},
			{Name: "second", Status: "failed", Error: "second original failure"},
		},
		Error: "two segments failed",
	}, nil
}

type failingExtractor struct {
	itemErr string
}

func (f *failingExtractor) Extract(_ context.Context, _ *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	return &youtubetypes.ExtractResponse{
		OK:        false,
		SourceURL: "https://www.youtube.com/watch?v=z_k1UGy4-qU",
		VideoID:   "z_k1UGy4-qU",
		Stats: &youtubetypes.ExtractStats{
			Requested: 1,
			Processed: 0,
			Skipped:   0,
			Failed:    1,
		},
		Items: []youtubetypes.ExtractItem{
			{Name: "probe-0", Start: "00:00", End: "00:05", Status: "failed", Error: f.itemErr},
		},
		Error: "one or more segments failed",
	}, nil
}
