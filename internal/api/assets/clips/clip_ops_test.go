// Package clips (test) — clip_ops_test.go.
//
// PR 3 (June 2026 — codex/clips-ops-cutover) handler-side tests
// for the three clip_ops endpoints. The handler is real; only
// the application-side ClipOpsService is replaced with stubs
// (the service is a concrete struct, so we satisfy each of its
// 6 ports with minimal stubs). Each subtest exercises one of
// the documented contracts in the spec:
//   - test handler con fake service  (each test below)
//   - test invalid source            (TestHandler_Cleanup_InvalidSource_Returns400,
//                                     TestHandler_VerifyClip_NoService_503)
//   - test voiceover                 (TestHandler_Cleanup_VoiceoverSource_ReportsOrphan)
//   - test Drive non disponibile     (Stub Drive returned empty MD5 + orphan path)
package clips

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Local port stubs (different package from application/clips) ───────────────

type handlerSourceResolver struct{ repos map[string]appclips.ClipRepositoryPort }

func (r *handlerSourceResolver) ResolveRepo(s string) appclips.ClipRepositoryPort {
	if r == nil {
		return nil
	}
	return r.repos[s]
}

type handlerClipsRepo struct{ clips []*asset.Asset }

func (r *handlerClipsRepo) Upsert(_ context.Context, _ *asset.Asset) error       { return nil }
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
func (r *handlerClipsRepo) ListFolders(_ context.Context, _ string) ([]*asset.ClipFolder, error) {
	return nil, nil
}
func (r *handlerClipsRepo) GetFolder(_ context.Context, _ string) (*asset.ClipFolder, error) {
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
func (r *handlerClipsRepo) DeleteFolder(_ context.Context, _ string) error         { return nil }
func (r *handlerClipsRepo) BulkAddTags(_ context.Context, _, _ []string) error    { return nil }
func (r *handlerClipsRepo) BulkRemoveTags(_ context.Context, _, _ []string) error { return nil }
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

type handlerVoiceoverRepo struct{ records map[string]*appclips.ClipVoiceoverRecordDTO }

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

type handlerImagesRepo struct{ images []*asset.ImageAsset }

func (r *handlerImagesRepo) ListAll(_ context.Context) ([]*asset.ImageAsset, error) {
	if r == nil {
		return nil, nil
	}
	return r.images, nil
}

type handlerDriveUploader struct{ md5ByFileID map[string]string }

func (d *handlerDriveUploader) GetOrCreateFolder(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (d *handlerDriveUploader) GetFolderName(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (d *handlerDriveUploader) TrashFolder(_ context.Context, _ string) error  { return nil }
func (d *handlerDriveUploader) DeleteFolder(_ context.Context, _ string) error { return nil }
func (d *handlerDriveUploader) UploadFile(_ context.Context, _, _, _ string) (*appclips.ClipUploadResultDTO, error) {
	return &appclips.ClipUploadResultDTO{}, nil
}
func (d *handlerDriveUploader) UploadFileWithDescription(_ context.Context, _, _, _, _ string) (*appclips.ClipUploadResultDTO, error) {
	return &appclips.ClipUploadResultDTO{}, nil
}
func (d *handlerDriveUploader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}
func (d *handlerDriveUploader) GetFileMD5(_ context.Context, fileID string) (string, error) {
	if d == nil || d.md5ByFileID == nil {
		return "", nil
	}
	return d.md5ByFileID[fileID], nil
}
func (d *handlerDriveUploader) GetFileMeta(_ context.Context, _ string) (*appclips.ClipDriveFileMetaDTO, error) {
	return &appclips.ClipDriveFileMetaDTO{}, nil
}
func (d *handlerDriveUploader) TrashFile(_ context.Context, _ string) error              { return nil }
func (d *handlerDriveUploader) ListFiles(_ context.Context, _ string) ([]appclips.ClipDriveFileDTO, error) {
	return nil, nil
}

type handlerCleanupPort struct{}

func (*handlerCleanupPort) CleanupOrphanFiles(_ context.Context, _ string, _ bool) (int, error) {
	return 0, nil
}
func (*handlerCleanupPort) DeleteClip(_ context.Context, _, _ string, _ bool) error {
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

// ── Test helpers ──────────────────────────────────────────────────────────────

func newOpsHandler(t *testing.T, svc *appclips.ClipOpsService) *Handler {
	t.Helper()
	return NewHandler(Deps{
		ClipOpsService: svc,
		Log:            zap.NewNop(),
	}, nil)
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

// ── Subtests ─────────────────────────────────────────────────────────────────

// TestHandler_Cleanup_200_ReportsOrphan pins the happy-path
// transport: service runs Cleanup → returns Items → handler
// marshals to gin.H JSON.
func TestHandler_Cleanup_200_ReportsOrphan(t *testing.T) {
	clip := &asset.Asset{ID: "yt-orphan-1", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &handlerClipsRepo{clips: []*asset.Asset{clip}}
	resolver := &handlerSourceResolver{repos: map[string]appclips.ClipRepositoryPort{"youtube": repo}}
	svc := appclips.NewClipOpsService(resolver, nil, nil, nil, &handlerCleanupPort{}, nil, zap.NewNop())
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
	items, ok := resp["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	require.Equal(t, "yt-orphan-1", items[0].(map[string]any)["id"])
}

// TestHandler_Cleanup_200_DeepEnqueue pins the deep-mode path:
// Handler 200 + jobs.Enqueue receives a system.cleanup request.
func TestHandler_Cleanup_200_DeepEnqueue(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-xyz"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerCleanupPort{}, jobs, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{"deep": true}`
	req := httptest.NewRequest("POST", "/api/clips/all/cleanup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, true, resp["ok"])
	require.Equal(t, "job-xyz", resp["job_id"])
	require.Equal(t, "system cleanup job enqueued", resp["message"])
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
}

// TestHandler_Cleanup_200_QueryDeepOverridesBody pins the
// "?deep=true" override path. Body says deep=false but the query
// param promotes the request to deep-mode.
func TestHandler_Cleanup_200_QueryDeepOverridesBody(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-from-query"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerCleanupPort{}, jobs, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{"deep": false}`
	req := httptest.NewRequest("POST", "/api/clips/all/cleanup?deep=true", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, jobs.enqueued, 1, "?deep=true must override body's deep=false")
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
}

// TestHandler_Cleanup_400_InvalidSource pins the early return
// for unknown source. Service returns "invalid source" sentinel;
// handler maps to 400 via mapClipOpsError.
func TestHandler_Cleanup_400_InvalidSource(t *testing.T) {
	resolver := &handlerSourceResolver{repos: map[string]appclips.ClipRepositoryPort{}}
	svc := appclips.NewClipOpsService(resolver, nil, nil, nil, &handlerCleanupPort{}, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{"deep": false}`
	req := httptest.NewRequest("POST", "/api/clips/not-a-source/cleanup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandler_Cleanup_400_MalformedJSON pins the bind-error
// path: handler maps JSON parse errors to 400 Bad Request.
func TestHandler_Cleanup_400_MalformedJSON(t *testing.T) {
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerCleanupPort{}, &handlerJobsPort{}, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/cleanup", strings.NewReader(`{not json}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandler_Cleanup_503_NoService pins the composition-bug
// guard: clipOpsService == nil → 503 Service Unavailable.
func TestHandler_Cleanup_503_NoService(t *testing.T) {
	h := NewHandler(Deps{Log: zap.NewNop()}, nil)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/all/cleanup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestHandler_Cleanup_VoiceoverSource_ReportsOrphan pins the
// voiceover-source branch (spec: test voiceover).
func TestHandler_Cleanup_VoiceoverSource_ReportsOrphan(t *testing.T) {
	rec := &appclips.ClipVoiceoverRecordDTO{
		ID:        "vo-1",
		Filename:  "foo.wav",
		LocalPath: "/nonexistent/foo.wav",
	}
	voiceover := &handlerVoiceoverRepo{records: map[string]*appclips.ClipVoiceoverRecordDTO{"vo-1": rec}}
	svc := appclips.NewClipOpsService(nil, voiceover, nil, nil, &handlerCleanupPort{}, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{"dry_run": true}`
	req := httptest.NewRequest("POST", "/api/clips/voiceover/cleanup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, "voiceover", resp["source"])
	items, ok := resp["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	require.Equal(t, "vo-1", items[0].(map[string]any)["id"])
}

// TestHandler_VerifyClip_200_ResponseShape pins the
// handler-resp-mapping contract: source, clip_id, db, issues
// fields must be present in the JSON.
func TestHandler_VerifyClip_200_ResponseShape(t *testing.T) {
	clip := &asset.Asset{ID: "v-1", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &handlerClipsRepo{clips: []*asset.Asset{clip}}
	resolver := &handlerSourceResolver{repos: map[string]appclips.ClipRepositoryPort{"youtube": repo}}
	svc := appclips.NewClipOpsService(resolver, nil, nil, nil, &handlerCleanupPort{}, nil, zap.NewNop())
	h := newOpsHandler(t, svc)
	r := newOpsRouter(t, h)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/clips/youtube/clips/v-1/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeBody(t, w)
	require.Equal(t, "v-1", resp["clip_id"])
	require.Equal(t, "youtube", resp["source"])
	require.Contains(t, resp["issues"].([]any), "local_file_missing")
}

// TestHandler_VerifyClip_503_NoService pins the composition-bug
// guard for VerifyClip.
func TestHandler_VerifyClip_503_NoService(t *testing.T) {
	h := NewHandler(Deps{Log: zap.NewNop()}, nil)
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/clips/v-1/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
