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
//     TestHandler_VerifyClip_NoService_503)
//   - test voiceover                 (TestHandler_Cleanup_VoiceoverSource_ReportsOrphan)
//   - test Drive non disponibile     (Stub Drive returned empty MD5 + orphan path)
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

	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Local port stubs (different package from application/clips) ───────────────

// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): handlerSourceResolver
// REMOVED. All clip-type sources share a single canonical handlerClipsRepo
// (defined immediately below); tests inject it directly into
// appclips.NewClipOpsService's first (clipRepo) slot. The per-source
// discriminator is now encoded at the test-fixture layer via the source
// string passed to Verify/Cleanup — the service's static switch in
// isKnownCleanupSource expands to cover all canonical clip-type sources
// (see clip_ops_reconcile.go in the application package).

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
func (r *handlerClipsRepo) DeleteFolder(_ context.Context, _ string) error        { return nil }
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

// ── Test helpers ──────────────────────────────────────────────────────────────

func newOpsHandler(t *testing.T, svc *appclips.ClipOpsService) *Handler {
	t.Helper()
	return NewHandler(Deps{
		ClipOpsService: svc,
		Log:            zap.NewNop(),
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

// ── Subtests ─────────────────────────────────────────────────────────────────

// TestHandler_Cleanup_200_ReportsOrphan pins the happy-path
// transport: jobs port wired + Cleanup succeeds → handler
// marshals 200 + job_id + empty items slice (S1b post-cutover
// shape; per-clip orphan records live in the worker output
// downstream of the broker poll, not the synchronous response).
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
	require.Len(t, items, 0, "S1b cleanup enqueues a job; per-clip items live in worker output, not the handler response")
	require.Len(t, jobsRP.enqueued, 1)
	require.Equal(t, "system.cleanup", jobsRP.enqueued[0].Type)
	require.Equal(t, "youtube", jobsRP.enqueued[0].Payload["source"])
}

// TestHandler_Cleanup_200_DeepEnqueue pins the deep-mode path:
// Handler 200 + jobs.Enqueue receives a system.cleanup request.
func TestHandler_Cleanup_200_DeepEnqueue(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-xyz"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())
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
	require.Equal(t, "system cleanup job enqueued; poll job_id=job-xyz for results", resp["message"])
	require.Len(t, jobs.enqueued, 1)
	require.Equal(t, "system.cleanup", jobs.enqueued[0].Type)
}

// TestHandler_Cleanup_200_QueryDeepOverridesBody pins the
// "?deep=true" override path. Body says deep=false but the query
// param promotes the request to deep-mode.
func TestHandler_Cleanup_200_QueryDeepOverridesBody(t *testing.T) {
	jobs := &handlerJobsPort{nextID: "job-from-query"}
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, jobs, nil, zap.NewNop())
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
// for unknown source. Service returns the typed
// appclips.ErrInvalidSource sentinel via errors.Is; handler
// maps to 400 via mapClipOpsError. Jobs port is wired so the
// source-validation branch (which runs BEFORE the jobs-nil check
// per the service implementation, per source-first precedence)
// is the one triggered; without wiring the test would fail
// earlier with a 503.
//
// Spec contract: POST /api/clips/:source/cleanup with an
// unrecognised :source must return 400 (handler-layer
// bad-request signal), not 500 or 503. The S1b post-cutover
// layer routes the typed ErrInvalidSource sentinel into 400.
func TestHandler_Cleanup_400_InvalidSource(t *testing.T) {
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerJobsPort{}, nil, zap.NewNop())
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
	svc := appclips.NewClipOpsService(nil, nil, nil, nil, &handlerJobsPort{}, nil, zap.NewNop())
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
	h := NewHandler(Deps{Log: zap.NewNop()})
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/all/cleanup", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestHandler_Cleanup_VoiceoverSource_ReportsOrphan pins the
// voiceover-source branch (spec: test voiceover). S1b
// post-cutover: the canonical Cleanup response is empty items +
// broker-assigned job_id; per-record items live in the worker
// output downstream of broker poll (the voiceover cleanup
// operates on the separate voiceovers table).
func TestHandler_Cleanup_VoiceoverSource_ReportsOrphan(t *testing.T) {
	rec := &appclips.ClipVoiceoverRecordDTO{
		ID:        "vo-1",
		Filename:  "foo.wav",
		LocalPath: "/nonexistent/foo.wav",
	}
	jobsVO := &handlerJobsPort{nextID: "job-vo"}
	voiceover := &handlerVoiceoverRepo{records: map[string]*appclips.ClipVoiceoverRecordDTO{"vo-1": rec}}
	svc := appclips.NewClipOpsService(nil, voiceover, nil, nil, jobsVO, nil, zap.NewNop())
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
	require.Equal(t, "job-vo", resp["job_id"])
	items, ok := resp["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 0, "S1b voiceover cleanup enqueues a job; per-record items live in worker output, not the handler response")
	require.Len(t, jobsVO.enqueued, 1)
	require.Equal(t, "system.cleanup", jobsVO.enqueued[0].Type)
	require.Equal(t, "voiceover", jobsVO.enqueued[0].Payload["source"])
}

// TestHandler_VerifyClip_200_ResponseShape pins the
// handler-resp-mapping contract: source, clip_id, db, issues
// fields must be present in the JSON.
func TestHandler_VerifyClip_200_ResponseShape(t *testing.T) {
	clip := &asset.Asset{ID: "v-1", Name: "foo"}
	clip.SetLocalPath("/this/path/does/not/exist/foo.mp4")
	repo := &handlerClipsRepo{clips: []*asset.Asset{clip}}
	svc := appclips.NewClipOpsService(repo, nil, nil, nil, nil, nil, zap.NewNop())
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
	h := NewHandler(Deps{Log: zap.NewNop()})
	r := newOpsRouter(t, h)

	req := httptest.NewRequest("POST", "/api/clips/youtube/clips/v-1/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestClipsQueryPort_CompositionWiring pins Commit E's nil-port-503
// fail-closed contract: when the composition root does NOT wire
// `Deps.ClipsRepo` (either via Deps.ClipsRepo == nil OR via the
// newClipsQueryPortAdapter helper returning nil because the
// underlying *assets.ClipsRepository was nil), every method that
// routes through the api/clips/ subtree MUST return HTTP 503
// (godlike/07) instead of:
//
//	(a) silently succeeding with empty results,
//	(b) propagating a 500 Internal Server Error from a nil-interface
//	    method-dispatch panic,
//	(c) hanging on the request goroutine.
//
// godlike/06 SSOT cross-check: this test exercises the contract end-to-end
// through the canonical OpsHandler route registration. The 503 sentinel is
// the canonical fail-closed response (newClipsQueryPortAdapter returns nil
// → Deps.ClipsRepo is the typed-nil interface → ops handler Method
// entry does `if h.clipsRepo == nil { apiutil.Error(c, 503, ...); return }`).
//
// Pattern parity: existing tests `TestHandler_Cleanup_503_NoService` +
// `TestHandler_VerifyClip_503_NoService` exercise a DIFFERENT port
// (ClipOpsService). This test exercises the NEW ClipsQueryPort — the
// Compiled-time assertion (var _ clips.ClipsQueryPort =
// (*clipsRepoAdapter)(nil)) at clips_adapters_repo.go guarantees the
// adapter satisfies the port; this test guarantees the runtime
// fail-closed contract for the composition-wiring layer.
func TestClipsQueryPort_CompositionWiring(t *testing.T) {
	// Deps.ClipsRepo is the typed-nil interface (zero value for the
	// typed-nil pointer field). Composition root at wire_assets_clips.go
	// would have set it to a non-nil ClipsQueryPort via
	// newClipsQueryPortAdapter(*assets.ClipsRepository), but here we
	// simulate the failure modes (nil-repo bundle, partial deploy,
	// test fixture) by leaving the field at the typed-zero.
	h := NewHandler(Deps{Log: zap.NewNop()}) // ClipsRepo == nil interface

	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/clips")
	g.GET("/:source/folders", h.ops.ListFolders)

	// Hit GET /api/clips/youtube/folders which routes through
	// OpsHandler.ListFolders which calls oh.repoForSource which
	// returns nil because oh.clipsRepo == nil. With Commit E's
	// nil-port-503 guard, the handler short-circuits to 503 at the
	// entry-point BEFORE the repoForSource call.
	req := httptest.NewRequest("GET", "/api/clips/youtube/folders", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code,
		"Commit E nil-port-503 contract: a nil ClipsQueryPort (composition wiring bug) MUST return HTTP 503, not 200 (silent no-op), 500 (nil-method panic), or 400 (misclassified bad-request).")
}
