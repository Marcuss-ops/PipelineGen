package clips

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Handler test doubles ───────────────────────────────────────────────

type handlerFakeDriveReader struct {
	meta *aistock.DriveFileMeta
	body io.ReadCloser
	ct   string
}

func (f *handlerFakeDriveReader) GetFileMeta(_ context.Context, _ string) (*aistock.DriveFileMeta, error) {
	return f.meta, nil
}

func (f *handlerFakeDriveReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return f.body, f.ct, nil
}

type handlerFakeArtifactService struct {
	err error
}

func (f *handlerFakeArtifactService) CreateAndVerify(_ context.Context, in appupload.ArtifactCreateInput) (*appupload.ArtifactRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &appupload.ArtifactRef{ID: "art-1", SHA256: "sha256-abc", SizeBytes: 1234}, nil
}

func (f *handlerFakeArtifactService) LocalPath(_ context.Context, _ string) (string, error) {
	return "/tmp/art-1.mp4", nil
}

type handlerFakeDispatcher struct{}

func (f *handlerFakeDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────────

func validAIStockDocument() string {
	return `{
		"schema_version": "ai_stock_visual_analysis.v1",
		"asset": {
			"proposed_asset_id": "underwater-sand-jumpscare-01",
			"source": "ai_generated",
			"asset_role": "stock",
			"media_type": "video",
			"folder_path": "Stock/AI/Ocean/MarineLife",
			"normalized_group": "stock",
			"title": "Pesce predatore nascosto sotto la sabbia",
			"duration_ms": 7000,
			"width": 1080,
			"height": 1920,
			"fps": 30,
			"has_audio": true,
			"has_dialogue": false,
			"audio_profile": "ambient_and_effects"
		},
		"visual_analysis": {
			"summary_en": "A hand brushes away ocean sand...",
			"summary_it": "Una mano sposta la sabbia...",
			"subjects": ["hand", "sand"],
			"environment": ["underwater"],
			"actions": ["brushing sand"]
		},
		"search_text": "Pesce predatore nascosto sotto la sabbia",
		"timed_events": [],
		"sound_cues": [],
		"recommended_clips": []
	}`
}

func newAIStockTestHandler(t *testing.T) *Handler {
	t.Helper()
	drive := &handlerFakeDriveReader{
		meta: &aistock.DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	uc, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: drive,
		Artifact:    &handlerFakeArtifactService{},
		Dispatcher:  &handlerFakeDispatcher{},
		Log:         zap.NewNop(),
	})
	require.NoError(t, err)
	return NewHandler(Deps{
		Ingest: IngestDeps{
			Dispatcher: &handlerFakeDispatcher{},
			AIStockUC:  uc,
			Log:        zap.NewNop(),
		},
	})
}

func TestCreateAIStockClip_NilUseCase_503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewHandler(Deps{
		Ingest: IngestDeps{
			Dispatcher: &handlerFakeDispatcher{},
			AIStockUC:  nil,
			Log:        zap.NewNop(),
		},
	})

	r := gin.New()
	r.POST("/api/clips/ingest/ai-stock", h.CreateAIStockClip)

	body, _ := json.Marshal(map[string]any{"document": json.RawMessage(validAIStockDocument()), "drive_url": "https://drive.google.com/file/d/ABC123/view"})
	req := httptest.NewRequest("POST", "/api/clips/ingest/ai-stock", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AI stock ingestion unavailable")
}

func TestCreateAIStockClip_InvalidBody_400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAIStockTestHandler(t)

	r := gin.New()
	r.POST("/api/clips/ingest/ai-stock", h.CreateAIStockClip)

	req := httptest.NewRequest("POST", "/api/clips/ingest/ai-stock", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateAIStockClip_ExecutionError_500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	drive := &handlerFakeDriveReader{
		meta: &aistock.DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	artifact := &handlerFakeArtifactService{}
	dispatcher := &handlerFakeDispatcher{}
	uc, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: drive,
		Artifact:    artifact,
		Dispatcher:  dispatcher,
		Log:         zap.NewNop(),
	})
	require.NoError(t, err)

	// Force artifact staging to fail, which propagates as a 500.
	artifact.err = assert.AnError

	h := NewHandler(Deps{
		Ingest: IngestDeps{
			Dispatcher: dispatcher,
			AIStockUC:  uc,
			Log:        zap.NewNop(),
		},
	})

	r := gin.New()
	r.POST("/api/clips/ingest/ai-stock", h.CreateAIStockClip)

	body, _ := json.Marshal(map[string]any{
		"document":  json.RawMessage(validAIStockDocument()),
		"drive_url": "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	req := httptest.NewRequest("POST", "/api/clips/ingest/ai-stock", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateAIStockClip_HappyPath_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAIStockTestHandler(t)

	r := gin.New()
	r.POST("/api/clips/ingest/ai-stock", h.CreateAIStockClip)

	body, _ := json.Marshal(map[string]any{
		"document":  json.RawMessage(validAIStockDocument()),
		"drive_url": "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	req := httptest.NewRequest("POST", "/api/clips/ingest/ai-stock", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, "underwater-sand-jumpscare-01", resp["clip_id"])
	assert.Equal(t, "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ", resp["drive_file_id"])
}

// fakeJobsSvc is a minimal job.Service stub for integration tests.
type fakeJobsSvc struct{}

func (fakeJobsSvc) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	return &job.Job{}, nil
}
func (fakeJobsSvc) Get(_ context.Context, _ string) (*job.Job, error)           { return nil, nil }
func (fakeJobsSvc) Cancel(_ context.Context, _ string) error                    { return nil }
func (fakeJobsSvc) List(_ context.Context, _ job.Filter) ([]job.Job, error)     { return nil, nil }
func (fakeJobsSvc) IsTerminal(_ job.Status) bool                                { return true }
func (fakeJobsSvc) RegisterHandler(_ string, _ any) error                       { return nil }
func (fakeJobsSvc) ListEvents(_ context.Context, _ string) ([]job.Event, error) { return nil, nil }
func (fakeJobsSvc) Retry(_ context.Context, _ string) (*job.Job, error)         { return nil, nil }

// TestClipsModule_AIStockClip_Integration exercises the full clips module
// built via clipsapi.Build and mounted under /api. It sends a complete HTTP
// request to /api/clips/ingest/ai-stock and verifies the route is reachable
// and returns 200 OK.
//
// Dependencies not exercised by the AI stock path are stubbed with nil-safe
// zero values: the repository is never queried, EnrichUC/BulkUploadWorker are
// only required to satisfy the fail-closed composition checks, and the job
// service is never invoked during this request.
func TestClipsModule_AIStockClip_Integration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	drive := &handlerFakeDriveReader{
		meta: &aistock.DriveFileMeta{Name: "underwater.mp4"},
		body: io.NopCloser(strings.NewReader("fake video bytes")),
		ct:   "video/mp4",
	}
	uc, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: drive,
		Artifact:    &handlerFakeArtifactService{},
		Dispatcher:  &handlerFakeDispatcher{},
		Log:         zap.NewNop(),
	})
	require.NoError(t, err)

	mod, err := Build(Dependencies{
		Handlers: Deps{
			Search: SearchDeps{
				ClipsRepo: assets.NewClipsRepository(nil, zap.NewNop()),
			},
			Ingest: IngestDeps{
				Dispatcher: &handlerFakeDispatcher{},
				AIStockUC:  uc,
				EnrichUC:   &appclips.EnrichUseCase{},
				Log:        zap.NewNop(),
			},
			NonOps: nonops.Deps{
				JobsSvc:          fakeJobsSvc{},
				BulkUploadWorker: appclips.NewBulkUploadWorker(nil, nil, nil, nil, nil, zap.NewNop()),
				Log:              zap.NewNop(),
			},
		},
		Transport: TransportDeps{
			EnabledFunc: func() bool { return true },
			Logger:      zap.NewNop(),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, mod)

	router := gin.New()
	mod.RegisterRoutes(router.Group("/api"))

	body, _ := json.Marshal(map[string]any{
		"document":  json.RawMessage(validAIStockDocument()),
		"drive_url": "https://drive.google.com/file/d/1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ/view",
	})
	req := httptest.NewRequest("POST", "/api/clips/ingest/ai-stock", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "POST /api/clips/ingest/ai-stock should return 200 through the full clips module")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["ok"].(bool))
	assert.Equal(t, "underwater-sand-jumpscare-01", resp["clip_id"])
	assert.Equal(t, "1fV3DmrHeqiZBIESZl-srEFn3jkp0PRlQ", resp["drive_file_id"])
}
