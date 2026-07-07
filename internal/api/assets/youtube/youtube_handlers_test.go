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

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

type recordingYouTubeClipService struct {
	folderCalls []folderCall
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

func (s *recordingYouTubeClipService) Extract(_ context.Context, _ *yttypes.ExtractRequest) (*yttypes.ExtractResponse, error) {
	return &yttypes.ExtractResponse{}, nil
}

func (s *recordingYouTubeClipService) GetOrCreateChannelFolder(_ context.Context, channelName, parentFolderID string) (string, error) {
	s.folderCalls = append(s.folderCalls, folderCall{name: channelName, parentID: parentFolderID})
	return parentFolderID + "/" + channelName, nil
}

type recordingJobsService struct {
	lastReq *jobservice.EnqueueRequest
}

func (s *recordingJobsService) Enqueue(_ context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error) {
	s.lastReq = req
	return &jobservice.Job{ID: "job-123"}, nil
}

func (s *recordingJobsService) Get(context.Context, string) (*jobservice.Job, error) { return nil, nil }
func (s *recordingJobsService) Cancel(context.Context, string) error                 { return nil }
func (s *recordingJobsService) List(context.Context, jobservice.Filter) ([]jobservice.Job, error) {
	return nil, nil
}
func (s *recordingJobsService) IsTerminal(status jobservice.Status) bool { return status.IsTerminal() }
func (s *recordingJobsService) RegisterHandler(string, any) error        { return nil }
func (s *recordingJobsService) ListEvents(context.Context, string) ([]jobservice.Event, error) {
	return nil, nil
}
func (s *recordingJobsService) Retry(context.Context, string) (*jobservice.Job, error) {
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
	require.Len(t, svc.folderCalls, 2)
	require.Equal(t, folderCall{name: "Pacquiao Vs Broner", parentID: "root-folder-id"}, svc.folderCalls[0])
	require.Equal(t, folderCall{name: "vdC5GXxS-qU", parentID: "root-folder-id/Pacquiao Vs Broner"}, svc.folderCalls[1])

	require.NotNil(t, jobsSvc.lastReq)
	payload, ok := jobsSvc.lastReq.Payload.(map[string]any)
	require.True(t, ok, "payload must be a JSON object map")

	dest, ok := payload["destination"].(map[string]any)
	require.True(t, ok, "destination must be present in payload")
	require.Equal(t, "root-folder-id/Pacquiao Vs Broner/vdC5GXxS-qU", dest["folder_id"])
	require.Equal(t, "Pacquiao Vs Broner/vdC5GXxS-qU", dest["folder_path"])
	require.Equal(t, true, dest["create_subfolder"])
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
	_, err := svc.Enqueue(context.Background(), &jobservice.EnqueueRequest{
		Type:    appjobs.TypeYouTubeClipExtract,
		Payload: map[string]any{"hello": "world"},
	})
	require.NoError(t, err)
	require.NotNil(t, svc.lastReq)
	require.Equal(t, "world", svc.lastReq.Payload.(map[string]any)["hello"])
}

func TestRecordingJobsService_IsTerminalDelegates(t *testing.T) {
	svc := &recordingJobsService{}
	require.True(t, svc.IsTerminal(jobservice.StatusSucceeded))
	require.False(t, svc.IsTerminal(jobservice.StatusQueued))
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
