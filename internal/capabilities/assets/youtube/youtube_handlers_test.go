package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type recordingYouTubeClipService struct {
	folderCalls []folderCall
	searchQuery string
	searchLimit int
	searchSort  string
}

type folderCall struct {
	name     string
	parentID string
}

func (s *recordingYouTubeClipService) Config() yttypes.RuntimeConfig {
	return yttypes.RuntimeConfig{}
}

func (s *recordingYouTubeClipService) GetVideoInfo(_ context.Context, _ string) (*ytports.DownloaderMetadata, error) {
	return &ytports.DownloaderMetadata{}, nil
}

func (s *recordingYouTubeClipService) SearchByTopicWithFilter(_ context.Context, query string, limit int, sort, _ string) (*youtube.TopicSearchResponse, error) {
	s.searchQuery = query
	s.searchLimit = limit
	s.searchSort = sort
	return &youtube.TopicSearchResponse{OK: true}, nil
}

func (s *recordingYouTubeClipService) Extract(_ context.Context, _ *yttypes.ExtractRequest) (*yttypes.ExtractResponse, error) {
	return &yttypes.ExtractResponse{}, nil
}

func (s *recordingYouTubeClipService) GetOrCreateChannelFolder(_ context.Context, channelName, parentFolderID string) (string, error) {
	s.folderCalls = append(s.folderCalls, folderCall{name: channelName, parentID: parentFolderID})
	return parentFolderID + "/" + channelName, nil
}

type recordingJobsService struct {
	lastReq *jobs.EnqueueRequest
}

func (s *recordingJobsService) Enqueue(_ context.Context, req *jobs.EnqueueRequest) (*jobs.Job, error) {
	s.lastReq = req
	return &jobs.Job{ID: "job-123"}, nil
}

func (s *recordingJobsService) Get(context.Context, string) (*jobs.Job, error) { return nil, nil }
func (s *recordingJobsService) Cancel(context.Context, string) error           { return nil }
func (s *recordingJobsService) List(context.Context, jobs.Filter) ([]jobs.Job, error) {
	return nil, nil
}
func (s *recordingJobsService) IsTerminal(status jobs.Status) bool { return status.IsTerminal() }
func (s *recordingJobsService) RegisterHandler(string, any) error  { return nil }
func (s *recordingJobsService) ListEvents(context.Context, string) ([]jobs.Event, error) {
	return nil, nil
}
func (s *recordingJobsService) Retry(context.Context, string) (*jobs.Job, error) {
	return nil, nil
}

func TestNormalizeExtractionDestination_DefaultsVideoSubfolder(t *testing.T) {
	group, subfolder, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		Group:    "Pacquiao Vs Broner",
		FolderID: "root-folder-id",
	}, "yt_vdC5GXxS-qU")

	require.Equal(t, "Pacquiao Vs Broner", group)
	require.Equal(t, "vdC5GXxS-qU", subfolder)
	require.Equal(t, path.Join("Pacquiao Vs Broner", "vdC5GXxS-qU"), folderPath)
	require.True(t, createSubfolder)
}

func TestYouTubeClipHandler_SearchByTopic_GetDelegatesKeywordAndOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &recordingYouTubeClipService{}
	handler := NewYouTubeClipHandler(svc, zap.NewNop(), nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/clips/search?q=Muhammad%20Ali%20boxing&limit=12&sort=views", nil)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.SearchByTopic(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Muhammad Ali boxing", svc.searchQuery)
	require.Equal(t, 12, svc.searchLimit)
	require.Equal(t, "views", svc.searchSort)
	require.JSONEq(t, `{"ok":true,"query":"","limit":0,"count":0,"source":"","results":null}`, rec.Body.String())
}

func TestYouTubeClipHandler_SearchByTopic_RejectsMissingQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewYouTubeClipHandler(&recordingYouTubeClipService{}, zap.NewNop(), nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/clips/search", nil)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.SearchByTopic(ctx)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "q is required")
}

func TestNormalizeExtractionDestination_FolderIDWithoutGroupStaysFlat(t *testing.T) {
	group, subfolder, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		FolderID: "root-folder-id",
	}, "yt_vdC5GXxS-qU")

	require.Empty(t, group)
	require.Empty(t, subfolder)
	require.Empty(t, folderPath)
	require.False(t, createSubfolder)
}

func TestNormalizeExtractionDestination_HonorsExplicitFlatFolder(t *testing.T) {
	group, subfolder, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		FolderID:        "youtube-stock-folder-id",
		FolderPath:      "Mike Tyson",
		CreateSubfolder: false,
	}, "yt_video-one")

	require.Empty(t, group)
	require.Empty(t, subfolder)
	require.Equal(t, "Mike Tyson", folderPath)
	require.False(t, createSubfolder)
}

func TestNormalizeExtractionDestination_FolderIDAloneStaysFlat(t *testing.T) {
	group, subfolder, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		FolderID: "explicit-drive-folder-id",
	}, "yt_video-one")

	require.Empty(t, group)
	require.Empty(t, subfolder)
	require.Empty(t, folderPath)
	require.False(t, createSubfolder, "a supplied folder ID is already the complete destination")
}

func TestYouTubeClipHandler_Extract_RejectsLegacyTopLevelDestinationFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	jobsSvc := &recordingJobsService{}
	handler := NewYouTubeClipHandler(
		&recordingYouTubeClipService{},
		zap.NewNop(),
		jobsSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	req := httptest.NewRequest(http.MethodPost, "/api/clips/process", bytes.NewBufferString(`{
		"url":"https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"segments":[{"start":"00:01:00","end":"00:01:10","name":"diagnostic"}],
		"folder_id":"legacy-folder-id",
		"folder_path":"Legacy Actor",
		"create_subfolder":false
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.Extract(ctx)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "destination fields must be nested under destination")
	require.Nil(t, jobsSvc.lastReq, "invalid legacy payload must not enqueue a job")
}

func TestYouTubeClipHandler_Extract_PreservesFlatDestinationFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	jobsSvc := &recordingJobsService{}
	handler := NewYouTubeClipHandler(
		&recordingYouTubeClipService{},
		zap.NewNop(),
		jobsSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	body := map[string]any{
		"url":      "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"segments": []map[string]any{{"start": "00:01:00", "end": "00:01:10", "name": "diagnostic"}},
		"destination": map[string]any{
			"folder_id":        "actor-folder-id",
			"folder_path":      "Tom Holland",
			"create_subfolder": false,
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/clips/process", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.Extract(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, jobsSvc.lastReq)
	payload, ok := jobsSvc.lastReq.Payload.(map[string]any)
	require.True(t, ok)

	dest, ok := payload["destination"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "actor-folder-id", dest["folder_id"])
	require.Equal(t, "Tom Holland", dest["folder_path"])
	require.Equal(t, false, dest["create_subfolder"])
	_, hasTopLevelFolderID := payload["folder_id"]
	_, hasTopLevelFolderPath := payload["folder_path"]
	_, hasTopLevelCreateSubfolder := payload["create_subfolder"]
	require.False(t, hasTopLevelFolderID)
	require.False(t, hasTopLevelFolderPath)
	require.False(t, hasTopLevelCreateSubfolder)
}

func TestYouTubeClipHandler_Extract_PreparesFolderPathAndPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &recordingYouTubeClipService{}
	jobsSvc := &recordingJobsService{}
	handler := NewYouTubeClipHandler(
		svc,
		zap.NewNop(),
		jobsSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	body := map[string]any{
		"url": "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"segments": []map[string]any{
			{
				"start": "00:02:26",
				"end":   "00:02:35",
				"name":  "Pensa a me, non a Floyd!",
			},
		},
		"destination": map[string]any{
			"group":     "Pacquiao Vs Broner",
			"folder_id": "root-folder-id",
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/clips/process", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.Extract(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, svc.folderCalls, "HTTP handler must not perform Drive folder creation synchronously")

	require.NotNil(t, jobsSvc.lastReq)
	payload, ok := jobsSvc.lastReq.Payload.(map[string]any)
	require.True(t, ok, "payload must be a JSON object map")

	dest, ok := payload["destination"].(map[string]any)
	require.True(t, ok, "destination must be present in payload")
	require.Equal(t, "root-folder-id", dest["folder_id"])
	require.Equal(t, "Pacquiao Vs Broner", dest["group"])
	require.Equal(t, "vdC5GXxS-qU", dest["subfolder_name"])
	require.Equal(t, "Pacquiao Vs Broner/vdC5GXxS-qU", dest["folder_path"])
	require.Equal(t, true, dest["create_subfolder"])
}

func TestYouTubeClipHandler_Extract_PreparesThreeSegmentPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &recordingYouTubeClipService{}
	jobsSvc := &recordingJobsService{}
	handler := NewYouTubeClipHandler(
		svc,
		zap.NewNop(),
		jobsSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	body := map[string]any{
		"url": "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"segments": []map[string]any{
			{
				"start": "01:05",
				"end":   "01:20",
				"name":  "Pacquiao talks about Mayweather in Japan",
			},
			{
				"start": "02:26",
				"end":   "02:35",
				"name":  "Broner says not to worry about Floyd",
			},
			{
				"start": "03:13",
				"end":   "03:25",
				"name":  "Broner jokes about hood support",
			},
		},
		"strategy": "verify",
		"destination": map[string]any{
			"group":            "Manny Pacquiao vs Adrien Broner",
			"folder_id":        "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ",
			"folder_path":      "Manny Pacquiao vs Adrien Broner",
			"subfolder_name":   "Manny Pacquiao vs Adrien Broner",
			"create_subfolder": true,
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/clips/process", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "yt-vdC5GXxS-qU-multi-clip")
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = req

	handler.Extract(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, jobsSvc.lastReq)
	require.Equal(t, appjobs.TypeYouTubeClipExtract, jobsSvc.lastReq.Type)

	payload, ok := jobsSvc.lastReq.Payload.(map[string]any)
	require.True(t, ok, "payload must be a JSON object map")

	segments, ok := payload["segments"].([]any)
	require.True(t, ok, "segments must be an array")
	require.Len(t, segments, 3)

	first := segments[0].(map[string]any)
	second := segments[1].(map[string]any)
	third := segments[2].(map[string]any)
	require.Equal(t, "01:05", first["start"])
	require.Equal(t, "01:20", first["end"])
	require.Equal(t, "02:26", second["start"])
	require.Equal(t, "02:35", second["end"])
	require.Equal(t, "03:13", third["start"])
	require.Equal(t, "03:25", third["end"])

	dest, ok := payload["destination"].(map[string]any)
	require.True(t, ok, "destination must be present in payload")
	require.Equal(t, "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ", dest["folder_id"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["group"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["subfolder_name"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["folder_path"])
	require.Equal(t, true, dest["create_subfolder"])
	require.Equal(t, "verify", payload["strategy"])
}

func TestNormalizeExtractionDestination_PreservesExplicitFolderPath(t *testing.T) {
	_, _, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		Group:      "Pacquiao Vs Broner",
		FolderID:   "root-folder-id",
		FolderPath: "youtube/Pacquiao-Vs-Broner/Press-Conference",
	}, "yt_vdC5GXxS-qU")

	require.Equal(t, "youtube/Pacquiao-Vs-Broner/Press-Conference", folderPath)
	require.True(t, createSubfolder)
}

func TestRecordingJobsService_EnqueueCapturesPayload(t *testing.T) {
	svc := &recordingJobsService{}
	_, err := svc.Enqueue(context.Background(), &jobs.EnqueueRequest{
		Type:    job.TypeYouTubeClipExtract,
		Payload: map[string]any{"hello": "world"},
	})
	require.NoError(t, err)
	require.NotNil(t, svc.lastReq)
	require.Equal(t, "world", svc.lastReq.Payload.(map[string]any)["hello"])
}

func TestRecordingJobsService_IsTerminalDelegates(t *testing.T) {
	svc := &recordingJobsService{}
	require.True(t, svc.IsTerminal(jobs.StatusSucceeded))
	require.False(t, svc.IsTerminal(jobs.StatusQueued))
}

func TestNormalizeExtractionDestination_InvalidsRemainEmpty(t *testing.T) {
	group, subfolder, folderPath, createSubfolder := normalizeExtractionDestination(&yttypes.DestinationRequest{
		Group:           "   ",
		FolderID:        "root-folder-id",
		SubfolderName:   "yt_",
		FolderPath:      "",
		CreateSubfolder: false,
	}, "")
	require.Empty(t, group)
	require.Empty(t, subfolder)
	require.Empty(t, folderPath)
	require.False(t, createSubfolder)
}
