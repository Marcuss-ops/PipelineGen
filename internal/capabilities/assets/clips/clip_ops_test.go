package clips

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type handlerClipsRepo struct{ clips []*asset.Asset }

func (r *handlerClipsRepo) Upsert(_ context.Context, _ *asset.Asset) error { return nil }
func (r *handlerClipsRepo) Get(_ context.Context, _ string) (*asset.Asset, error) {
	return nil, nil
}
func (r *handlerClipsRepo) GetClip(_ context.Context, id string) (*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	for _, c := range r.clips {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, nil
}
func (r *handlerClipsRepo) ListFolders(_ context.Context, _ string) ([]*detail.ClipFolder, error) {
	return nil, nil
}
func (r *handlerClipsRepo) GetFolder(_ context.Context, _ string) (*detail.ClipFolder, error) {
	return nil, nil
}
func (r *handlerClipsRepo) GetFolderChildren(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *handlerClipsRepo) ListByFolderID(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *handlerClipsRepo) ListByFolderPath(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}
func (r *handlerClipsRepo) DeleteFolder(_ context.Context, _ string) error { return nil }
func (r *handlerClipsRepo) ListClipsPaged(_ context.Context, _ string, _, _ int, _ string) ([]*asset.Asset, error) {
	if r == nil {
		return nil, nil
	}
	out := make([]*asset.Asset, 0, len(r.clips))
	return append(out, r.clips...), nil
}
func (r *handlerClipsRepo) FindClipsByHash(_ context.Context, _ string) ([]*asset.Asset, error) {
	return nil, nil
}

type handlerVoiceoverRepo struct {
	records map[string]*appclips.ClipVoiceoverRecordDTO
}

func (r *handlerVoiceoverRepo) GetByID(_ context.Context, id string) (*appclips.ClipVoiceoverRecordDTO, error) {
	if r == nil {
		return nil, nil
	}
	return r.records[id], nil
}
func (r *handlerVoiceoverRepo) ListAll(_ context.Context) ([]*appclips.ClipVoiceoverRecordDTO, error) {
	if r == nil {
		return nil, nil
	}
	out := make([]*appclips.ClipVoiceoverRecordDTO, 0, len(r.records))
	for _, rec := range r.records {
		out = append(out, rec)
	}
	return out, nil
}
func (r *handlerVoiceoverRepo) Upsert(_ context.Context, _ *appclips.ClipVoiceoverRecordDTO) error {
	return nil
}

type handlerJobsPort struct {
	enqueued []appclips.JobsEnqueueRequest
	nextID   string
}

func (j *handlerJobsPort) Enqueue(_ context.Context, req appclips.JobsEnqueueRequest) (*appclips.JobsEnqueueResponse, error) {
	if j == nil {
		return nil, nil
	}
	j.enqueued = append(j.enqueued, req)
	if j.nextID == "" {
		j.nextID = "stub-job-id"
	}
	return &appclips.JobsEnqueueResponse{ID: j.nextID}, nil
}

func newOpsHandler(t *testing.T, svc *appclips.ClipOpsService) *Handler {
	t.Helper()
	return NewHandler(Deps{
		Operations: OpsDeps{
			ClipOpsService: svc,
			Log:            zap.NewNop(),
		},
	})
}

func newOpsRouter(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/clips")
	g.POST("/:source/cleanup", h.Cleanup)
	g.POST("/:source/clips/:id/verify", h.VerifyClip)
	g.POST("/:source/reconcile", h.Reconcile)
	return r
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func TestHandler_Cleanup_200_ReportsOrphan(t *testing.T) {
	clip := &asset.Asset{ID: "yt-orphan-1", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &handlerClipsRepo{clips: []*asset.Asset{clip}}
	jobsRP := &handlerJobsPort{nextID: "job-rp"}
	svc := appclips.NewClipOpsService(repo, nil, nil, nil, jobsRP, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{"dry_run": true, "deep": false, "check_drive": false}`
	req := httptest.NewRequest("POST", "/api/clips/youtube/cleanup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, true, resp["ok"])
	require.Equal(t, "youtube", resp["source"])
	require.Equal(t, true, resp["dry_run"])
	require.Equal(t, "job-rp", resp["job_id"])
	items, ok := resp["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 0)
	require.Len(t, jobsRP.enqueued, 1)
	require.Equal(t, "system.cleanup", jobsRP.enqueued[0].Type)
	require.Equal(t, "youtube", jobsRP.enqueued[0].Payload["source"])
}

func TestHandler_Cleanup_200_DeepEnqueue(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-xyz"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/all/cleanup", strings.NewReader(`{"deep": true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, true, resp["ok"])
	require.Equal(t, "job-xyz", resp["job_id"])
	require.Equal(t, "system cleanup job enqueued; poll job_id=job-xyz for results", resp["message"])
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
}

func TestHandler_Cleanup_200_QueryDeepOverridesBody(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-from-query"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/all/cleanup?deep=true", strings.NewReader(`{"deep": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
}

func TestHandler_Cleanup_400_InvalidSource(t *testing.T) {
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerJobsPort{}, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/not-a-source/cleanup", strings.NewReader(`{"deep": false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Cleanup_400_MalformedJSON(t *testing.T) {
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerJobsPort{}, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/cleanup", strings.NewReader(`{not json}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_Cleanup_503_NoService(t *testing.T) {
	h := NewHandler(Deps{Operations: OpsDeps{Log: zap.NewNop()}})
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/all/cleanup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandler_Cleanup_VoiceoverSource_ReportsOrphan(t *testing.T) {
	rec := &appclips.ClipVoiceoverRecordDTO{ID: "vo-1", Filename: "foo.wav", LocalPath: "/nonexistent/foo.wav"}
	jobsVO := &handlerJobsPort{nextID: "job-vo"}
	voiceover := &handlerVoiceoverRepo{records: map[string]*appclips.ClipVoiceoverRecordDTO{"vo-1": rec}}
	svc := appclips.NewClipOpsService(nil, voiceover, nil, nil, jobsVO, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/voiceover/cleanup", strings.NewReader(`{"dry_run": true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, "voiceover", resp["source"])
	require.Equal(t, "job-vo", resp["job_id"])
	items, ok := resp["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 0)
	require.Len(t, jobsVO.enqueued, 1)
	require.Equal(t, "system.cleanup", jobsVO.enqueued[0].Type)
	require.Equal(t, "voiceover", jobsVO.enqueued[0].Payload["source"])
}

func TestHandler_VerifyClip_200_ResponseShape(t *testing.T) {
	clip := &asset.Asset{ID: "v-1", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &handlerClipsRepo{clips: []*asset.Asset{clip}}
	svc := appclips.NewClipOpsService(repo, nil, nil, nil, nil, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/clips/v-1/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, "v-1", resp["clip_id"])
	require.Equal(t, "youtube", resp["source"])
	require.Contains(t, resp["issues"].([]any), "local_file_missing")
}

func TestHandler_VerifyClip_503_NoService(t *testing.T) {
	h := NewHandler(Deps{Operations: OpsDeps{Log: zap.NewNop()}})
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/clips/v-1/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestClipsQueryPort_CompositionWiring(t *testing.T) {
	h := NewHandler(Deps{Operations: OpsDeps{Log: zap.NewNop()}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/clips")
	g.GET("/:source/folders", h.ops.ListFolders)

	req := httptest.NewRequest("GET", "/api/clips/youtube/folders", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
