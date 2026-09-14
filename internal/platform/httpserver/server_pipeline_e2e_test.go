// Package httpserver_test — server_pipeline_e2e_test.go
//
// HERMETIC (in-process) contract battery for the 10-step operational
// pipeline gate. This is the always-runnable half of the gate; the live
// half is tests/operational/pipeline_live_e2e.sh (`make verify-pipeline-e2e-live`).
//
// Why both halves exist (godlike/07 honest scope):
//
//   - A hermetic test CAN prove the wiring: the real gin router, the real
//     YouTube clip handler, the real unified-search handler over a real
//     search.Aggregator, the real stock handler over the real StockUseCase,
//     and the real Stripe-style idempotency middleware backed by the real
//     SQLite store.
//   - A hermetic test CANNOT prove reality: it cannot assert that a Drive
//     file exists under the requested folder, that an MP4 is >100 KB, or
//     that the committed asset is retrievable from the PostgreSQL catalog.
//     Those assertions live ONLY in the live shell battery, never here.
//   - Therefore step 8 below pins the Drive-artifact CONTRACT SHAPE and
//     explicitly does not claim a Drive artifact was produced.
//
// Steps (mirrors tests/operational/pipeline_live_e2e.sh):
//
//	01 clips/search         — live YouTube keyword discovery
//	02 clips/info           — URL → metadata
//	03 clips/process        — accepted + real job enqueued; legacy top-level
//	                          destination fields are rejected, never enqueued
//	04 destination          — group + create_subfolder derives the per-video
//	   normalization          subfolder (no Drive I/O at the HTTP boundary)
//	05 media/search         — canonical unified search envelope
//	06 stock-pipeline/run   — direct_urls → QUEUED + broker job
//	07 stock-pipeline/      — search-and-run takes `queries:[{q,limit}]`
//	   search-and-run         (`search_queries` is the legacy `/run` shape)
//	08 Drive artifact       — ExtractItem can carry the Drive identity triple
//	09 clip download route  — POST /api/media/clips/:source/clips/:id/download wired
//	10 idempotency replay   — same key + same body → no second enqueue
package httpserver_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	clipshandler "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips"
	clipspublication "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/publication"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/stock"
	cliphttp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/youtube"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	httpserver "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	idempotency "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/idempotency"
)

const (
	pipelineE2EVideoURL  = "https://www.youtube.com/watch?v=vdC5GXxS-qU"
	pipelineE2EVideoID   = "vdC5GXxS-qU"
	pipelineE2EDriveRoot = "e2e-drive-root-folder"
	pipelineE2EDirectURL = "https://cdn.example.com/e2e/test-video.mp4"
	pipelineE2EGroup     = "e2e-run-tag"
)

// ── Stub external edges ─────────────────────────────────────────────────
//
// Only the traffic that would leave the process is stubbed: the YouTube
// provider, the jobs broker, the search backend and the stock runner. Every
// HTTP handler, validation chain, aggregator and middleware under test is
// the production implementation.

type pipelineE2EYouTube struct {
	mu          sync.Mutex
	searchCalls int
}

func (s *pipelineE2EYouTube) Config() yttypes.RuntimeConfig { return yttypes.RuntimeConfig{} }

func (s *pipelineE2EYouTube) GetVideoInfo(_ context.Context, videoURL string) (*ytports.DownloaderMetadata, error) {
	return &ytports.DownloaderMetadata{
		ID:       pipelineE2EVideoID,
		Title:    "Muhammad Ali boxing highlights",
		Duration: 512.5,
		URL:      videoURL,
		Uploader: "e2e-channel",
	}, nil
}

func (s *pipelineE2EYouTube) SearchByTopicWithFilter(_ context.Context, q string, limit int, _, _ string) (*youtube.TopicSearchResponse, error) {
	s.mu.Lock()
	s.searchCalls++
	s.mu.Unlock()
	if limit <= 0 {
		limit = 5
	}
	return &youtube.TopicSearchResponse{
		OK:     true,
		Query:  q,
		Limit:  limit,
		Count:  1,
		Source: "youtube",
		Results: []youtube.TopicSearchResult{{
			VideoID:     pipelineE2EVideoID,
			Title:       "Muhammad Ali boxing highlights",
			ChannelName: "e2e-channel",
			Duration:    512,
		}},
	}, nil
}

func (s *pipelineE2EYouTube) Extract(context.Context, *yttypes.ExtractRequest) (*yttypes.ExtractResponse, error) {
	return &yttypes.ExtractResponse{}, nil
}

func (s *pipelineE2EYouTube) GetOrCreateChannelFolder(_ context.Context, channelName, parentFolderID string) (string, error) {
	return parentFolderID + "/" + channelName, nil
}

// pipelineE2EJobs implements both kernel job.Service (consumed by the
// YouTube handler) and the narrowed jobsEnqueuer of StockUseCase.
type pipelineE2EJobs struct {
	mu       sync.Mutex
	requests []*job.EnqueueRequest
	nextID   int
}

func (s *pipelineE2EJobs) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	cp := *req
	s.requests = append(s.requests, &cp)
	return &job.Job{ID: fmt.Sprintf("e2e-job-%02d", s.nextID), Type: req.Type}, nil
}

func (s *pipelineE2EJobs) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *pipelineE2EJobs) last(t *testing.T) *job.EnqueueRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.requests, "expected at least one enqueue")
	return s.requests[len(s.requests)-1]
}

func (s *pipelineE2EJobs) Get(context.Context, string) (*job.Job, error) { return nil, nil }
func (s *pipelineE2EJobs) Cancel(context.Context, string) error          { return nil }
func (s *pipelineE2EJobs) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *pipelineE2EJobs) IsTerminal(status job.Status) bool { return status.IsTerminal() }
func (s *pipelineE2EJobs) RegisterHandler(string, any) error { return nil }
func (s *pipelineE2EJobs) ListEvents(context.Context, string) ([]job.Event, error) {
	return nil, nil
}
func (s *pipelineE2EJobs) Retry(context.Context, string) (*job.Job, error) { return nil, nil }

// pipelineE2EBackend is a deterministic in-process search backend. It lets
// the test assert what the REAL Aggregator and the REAL /api/media/search
// handler forwarded downstream without touching pgvector.
type pipelineE2EBackend struct {
	mu      sync.Mutex
	queries []search.Query
}

func (b *pipelineE2EBackend) Name() string { return "youtube" }

func (b *pipelineE2EBackend) Capabilities() []search.Capability {
	return []search.Capability{search.Capability("video")}
}

func (b *pipelineE2EBackend) Universe() search.SearchUniverse { return search.SearchCatalog }

func (b *pipelineE2EBackend) Search(_ context.Context, q search.Query) ([]search.Candidate, error) {
	b.mu.Lock()
	b.queries = append(b.queries, q)
	b.mu.Unlock()
	return []search.Candidate{{
		AssetID:    "yt_" + pipelineE2EVideoID,
		Source:     "youtube",
		SourceRef:  pipelineE2EVideoID,
		MediaType:  "video",
		Title:      q.Text,
		SourceURL:  pipelineE2EVideoURL,
		DurationMs: 512500,
		Score:      0.93,
	}}, nil
}

func (b *pipelineE2EBackend) lastQuery(t *testing.T) search.Query {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	require.NotEmpty(t, b.queries, "expected the aggregator to fan out to the backend")
	return b.queries[len(b.queries)-1]
}

// pipelineE2EStockRunner is the sync-path stand-in for the ffmpeg/render
// composition graph.
type pipelineE2EStockRunner struct {
	mu   sync.Mutex
	runs []*stockpipeline.RunInput
}

func (r *pipelineE2EStockRunner) Run(_ context.Context, in *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	r.mu.Lock()
	r.runs = append(r.runs, in)
	r.mu.Unlock()
	return &stockpipeline.PipelineResult{TotalClips: 1, TotalChunks: 1}, nil
}

// ── Harness ─────────────────────────────────────────────────────────────

type pipelineE2EHarness struct {
	router  *gin.Engine
	jobs    *pipelineE2EJobs
	youtube *pipelineE2EYouTube
	backend *pipelineE2EBackend
	runner  *pipelineE2EStockRunner
}

const pipelineE2EIdempotencyDDL = `
CREATE TABLE IF NOT EXISTS idempotency_keys (
	key TEXT PRIMARY KEY,
	body_hash TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'in_flight',
	response_status INTEGER NOT NULL DEFAULT 0,
	response_body TEXT NOT NULL DEFAULT '',
	response_content_type TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	last_replayed_at TEXT NOT NULL DEFAULT ''
);`

func newPipelineE2EHarness(t *testing.T) *pipelineE2EHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{DataDir: dataDir},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: filepath.Join(dataDir, "downloads"),
		},
	}

	// Real idempotency middleware over the real SQLite store.
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "idempotency.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(pipelineE2EIdempotencyDDL)
	require.NoError(t, err)
	idemMW := middleware.NewIdempotency(idempotency.NewSQLiteRepository(db), zap.NewNop())
	t.Cleanup(idemMW.Stop)

	jobs := &pipelineE2EJobs{}
	yt := &pipelineE2EYouTube{}
	backend := &pipelineE2EBackend{}
	runner := &pipelineE2EStockRunner{}

	// Real YouTube clip handler (clips/search, clips/info, clips/process).
	ytHandler := cliphttp.NewYouTubeClipHandler(
		yt, zap.NewNop(), jobs, nil, nil, idemMW.Handler(), nil, nil,
	)

	// Real stock handler over the real StockUseCase (async → broker job).
	stockUC := stockpipeline.NewStockUseCase(runner, jobs, zap.NewNop())
	stockHandler := stock.NewStockHandler(stockUC, zap.NewNop())

	// Real unified-search handler over a real Aggregator + one backend.
	aggregator := search.NewAggregator(func() *search.BackendRegistry {
		reg := search.NewBackendRegistry()
		require.NoError(t, reg.Register(backend))
		return reg
	}(), nil)
	searchHandler := search.NewHandler(aggregator, nil, zap.NewNop())

	// Real publication descriptor (canonical download route table).
	publication, err := clipspublication.Build(clipspublication.Dependencies{
		Publication: clipshandler.NewActionHandler(clipshandler.ActionDeps{Log: zap.NewNop()}),
		EnabledFunc: func() bool { return true },
		Idempotency: idemMW.Handler(),
		Logger:      zap.NewNop(),
	})
	require.NoError(t, err)

	enabled := func() bool { return true }
	registry := httpserver.NewRegistry()
	require.NoError(t, registry.Register(httpserver.NewRouteModule("clips", enabled, "/clips", ytHandler, zap.NewNop())))
	require.NoError(t, registry.Register(httpserver.NewRouteModule("stock-pipeline", enabled, "/stock-pipeline", stockHandler, zap.NewNop())))
	require.NoError(t, registry.Register(httpserver.NewRouteModule("media-search", enabled, "/media", searchHandler, zap.NewNop())))
	// The clips capability mounts under the canonical /api/media/clips wire
	// prefix (transport/wire.go), NOT directly under /api/media. Mounting it at
	// "/media" here certified a route shape production does not serve.
	require.NoError(t, registry.Register(httpserver.NewRouteModule("clips-publication", enabled, "/media/clips", publication, zap.NewNop())))

	server := httpserver.NewServerWithHealth(httpserver.ServerDeps{Config: cfg, Registry: registry})
	return &pipelineE2EHarness{
		router:  server.GetRouter(),
		jobs:    jobs,
		youtube: yt,
		backend: backend,
		runner:  runner,
	}
}

func (h *pipelineE2EHarness) do(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func pipelineE2EDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

// pipelineE2EProcessPayload is the canonical clips/process body.
func pipelineE2EProcessPayload(group string) map[string]any {
	return map[string]any{
		"url": pipelineE2EVideoURL,
		"segments": []map[string]any{{
			"start": "00:00:05",
			"end":   "00:00:12",
			"name":  pipelineE2EGroup + "-youtube",
		}},
		"strategy": "verify",
		"destination": map[string]any{
			"folder_id":        pipelineE2EDriveRoot,
			"group":            group,
			"create_subfolder": true,
		},
	}
}

// pipelineE2EStockDurationContract is the explicit per-source duration
// contract (all four fields must be present and mutually consistent).
func pipelineE2EStockDurationContract() map[string]any {
	return map[string]any{
		"target_total_duration_seconds":      10,
		"target_duration_per_source_seconds": 5,
		"clips_per_source":                   1,
		"clip_duration_seconds":              5,
		"download_mode":                      "sections_only",
	}
}

// ── The 10 steps ────────────────────────────────────────────────────────

func TestPipelineE2E(t *testing.T) {
	h := newPipelineE2EHarness(t)

	t.Run("01_clips_search_returns_video_identity", func(t *testing.T) {
		rec := h.do(t, http.MethodGet,
			"/api/clips/search?q=Muhammad%20Ali%20boxing&limit=10&sort=views", nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := pipelineE2EDecode(t, rec)
		require.Equal(t, true, body["ok"])
		require.Equal(t, "youtube", body["source"])
		results, ok := body["results"].([]any)
		require.True(t, ok, "results must be an array")
		require.NotEmpty(t, results, "keyword search must return at least one candidate")
		first, ok := results[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, pipelineE2EVideoID, first["video_id"], "candidate must carry a usable video identity")
		require.NotEmpty(t, first["title"])

		// A missing query is a typed 400, never a 200 with an empty list.
		missing := h.do(t, http.MethodGet, "/api/clips/search", nil, nil)
		require.Equal(t, http.StatusBadRequest, missing.Code, missing.Body.String())
	})

	t.Run("02_clips_info_resolves_url_parameter", func(t *testing.T) {
		rec := h.do(t, http.MethodGet, "/api/clips/info?url="+url.QueryEscape(pipelineE2EVideoURL), nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := pipelineE2EDecode(t, rec)
		require.Equal(t, pipelineE2EVideoID, body["id"], "info must resolve the video identity")
		require.NotEmpty(t, body["title"])
		require.Greater(t, body["duration"], float64(0), "duration metadata must be present")

		// The query parameter is exactly `url`.
		missing := h.do(t, http.MethodGet, "/api/clips/info", nil, nil)
		require.Equal(t, http.StatusBadRequest, missing.Code, missing.Body.String())
	})

	t.Run("03_clips_process_enqueues_and_rejects_legacy_destination", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/clips/process", pipelineE2EProcessPayload(pipelineE2EGroup),
			map[string]string{"Idempotency-Key": "e2e-process-accept"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := pipelineE2EDecode(t, rec)
		require.Equal(t, true, body["ok"])

		require.Equal(t, 1, h.jobs.count())
		enqueued := h.jobs.last(t)
		require.Equal(t, appjobs.TypeYouTubeClipExtract, enqueued.Type)
		payload, ok := enqueued.Payload.(map[string]any)
		require.True(t, ok, "payload must be a JSON object")
		segments, ok := payload["segments"].([]any)
		require.True(t, ok)
		require.Len(t, segments, 1)
		require.Equal(t, pipelineE2EVideoURL, payload["url"])

		// Legacy top-level destination fields are rejected and never enqueued.
		legacy := map[string]any{
			"url":              pipelineE2EVideoURL,
			"segments":         []map[string]any{{"start": "00:00:05", "end": "00:00:12", "name": "legacy"}},
			"folder_id":        pipelineE2EDriveRoot,
			"create_subfolder": true,
		}
		rejected := h.do(t, http.MethodPost, "/api/clips/process", legacy,
			map[string]string{"Idempotency-Key": "e2e-process-legacy"})
		require.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
		require.Contains(t, rejected.Body.String(), "destination fields must be nested under destination")
		require.Equal(t, 1, h.jobs.count(), "a rejected payload must not enqueue a job")
	})

	t.Run("04_destination_normalization_derives_video_subfolder", func(t *testing.T) {
		rec := h.do(t, http.MethodPost, "/api/clips/process", pipelineE2EProcessPayload(pipelineE2EGroup),
			map[string]string{"Idempotency-Key": "e2e-process-normalize"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		payload, ok := h.jobs.last(t).Payload.(map[string]any)
		require.True(t, ok)
		dest, ok := payload["destination"].(map[string]any)
		require.True(t, ok, "destination must survive normalization")
		require.Equal(t, pipelineE2EGroup, dest["group"])
		require.Equal(t, pipelineE2EDriveRoot, dest["folder_id"])
		require.Equal(t, true, dest["create_subfolder"])
		// group + create_subfolder derives the per-video subfolder from the URL.
		require.Equal(t, pipelineE2EVideoID, dest["subfolder_name"],
			"the worker must receive the per-video subfolder, not the raw group")
		require.Equal(t, pipelineE2EGroup+"/"+pipelineE2EVideoID, dest["folder_path"])
	})

	t.Run("05_media_search_canonical_envelope", func(t *testing.T) {
		body := map[string]any{
			"query":    pipelineE2EGroup + "-youtube",
			"sources":  []string{"youtube"},
			"mode":     "hybrid",
			"universe": "catalog",
			"filters":  map[string]any{"media_type": "video"},
			"limit":    20,
		}
		rec := h.do(t, http.MethodPost, "/api/media/search", body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		out := pipelineE2EDecode(t, rec)
		items, ok := out["items"].([]any)
		require.True(t, ok, "canonical envelope must expose items")
		require.NotEmpty(t, items)
		first, ok := items[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "youtube", first["source"])
		require.NotEmpty(t, first["asset_id"], "canonical asset identity is mandatory")

		// The handler forwarded the exact structured query downstream.
		q := h.backend.lastQuery(t)
		require.Equal(t, []string{"youtube"}, q.Sources)
		require.Equal(t, search.SearchModeHybrid, q.Mode)
		require.Equal(t, search.SearchCatalog, q.EffectiveUniverse())
		require.Equal(t, "video", q.Filters.MediaType)
		require.Equal(t, 20, q.Limit)

		empty := h.do(t, http.MethodPost, "/api/media/search", map[string]any{"query": "   "}, nil)
		require.Equal(t, http.StatusBadRequest, empty.Code, empty.Body.String())
	})

	t.Run("06_stock_run_direct_url_queues_broker_job", func(t *testing.T) {
		body := pipelineE2EStockDurationContract()
		body["direct_urls"] = []string{pipelineE2EDirectURL}
		body["drive_folder_id"] = pipelineE2EDriveRoot
		body["folder_name"] = pipelineE2EGroup + "-stock"
		body["subfolder"] = "direct"
		body["async"] = true
		body["persist"] = true

		rec := h.do(t, http.MethodPost, "/api/stock-pipeline/run", body, nil)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		out := pipelineE2EDecode(t, rec)
		require.Equal(t, stock.StatusPending, out["status"])
		require.NotEmpty(t, out["job_id"])
		require.Equal(t, false, out["deduplicated"], "first submission must not claim dedup")

		enqueued := h.jobs.last(t)
		require.Equal(t, "media.stock", enqueued.Type)

		// An incomplete explicit duration contract is refused, not silently
		// downgraded to the legacy shape.
		partial := map[string]any{
			"direct_urls":                   []string{pipelineE2EDirectURL},
			"target_total_duration_seconds": 10,
			"clip_duration_seconds":         5,
		}
		bad := h.do(t, http.MethodPost, "/api/stock-pipeline/run", partial, nil)
		require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
		require.Contains(t, bad.Body.String(), "explicit stock duration contract requires")
	})

	t.Run("07_stock_search_and_run_uses_queries_shape", func(t *testing.T) {
		body := pipelineE2EStockDurationContract()
		body["queries"] = []map[string]any{{"q": pipelineE2EGroup + " city skyline", "limit": 5}}
		body["drive_folder_id"] = pipelineE2EDriveRoot
		body["folder_name"] = pipelineE2EGroup + "-stock-search"
		body["async"] = true

		rec := h.do(t, http.MethodPost, "/api/stock-pipeline/search-and-run", body, nil)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		out := pipelineE2EDecode(t, rec)
		require.Equal(t, stock.StatusPending, out["status"])
		require.NotEmpty(t, out["job_id"])

		enqueued := h.jobs.last(t)
		require.Equal(t, "media.stock", enqueued.Type)
		raw, err := json.Marshal(enqueued.Payload)
		require.NoError(t, err)
		require.Contains(t, string(raw), pipelineE2EGroup+" city skyline",
			"the search term must survive the wire round-trip into the job payload")

		// `search_queries` is the legacy /run shape: bound here it is ignored,
		// so the source-presence gate fires instead of running an empty job.
		legacy := map[string]any{"search_queries": []string{"e2e legacy"}, "async": true}
		bad := h.do(t, http.MethodPost, "/api/stock-pipeline/search-and-run", legacy, nil)
		require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
		require.Contains(t, bad.Body.String(), "at least one of queries")
	})

	t.Run("08_drive_artifact_contract_shape", func(t *testing.T) {
		// SHAPE ONLY. The live battery asserts a real Drive file exists under
		// the requested folder; this test only pins that the artifact
		// contract can carry the Drive identity triple at all.
		item := yttypes.ExtractItem{
			Name:            "clip.mp4",
			Status:          "processed",
			DriveFileID:     "drive-file-id",
			DriveLink:       "https://drive.google.com/file/d/drive-file-id/view",
			DriveFolderID:   "drive-folder-id",
			DriveFolderPath: pipelineE2EGroup + "/" + pipelineE2EVideoID,
		}
		raw, err := json.Marshal(item)
		require.NoError(t, err)
		for _, key := range []string{"drive_file_id", "drive_link", "drive_folder_id", "drive_folder_path"} {
			require.Contains(t, string(raw), key, "ExtractItem must expose %s", key)
		}

		// The enqueued process payload carries the destination the worker
		// resolves into that hierarchy. Re-submit here so the assertion does
		// not depend on which step ran last.
		rec := h.do(t, http.MethodPost, "/api/clips/process", pipelineE2EProcessPayload(pipelineE2EGroup),
			map[string]string{"Idempotency-Key": "e2e-process-artifact-contract"})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		payload, ok := h.jobs.last(t).Payload.(map[string]any)
		require.True(t, ok, "the enqueued payload must be a JSON object")
		dest, ok := payload["destination"].(map[string]any)
		require.True(t, ok, "the enqueued payload must carry the destination")
		require.Equal(t, pipelineE2EDriveRoot, dest["folder_id"])
		require.Equal(t, pipelineE2EGroup+"/"+pipelineE2EVideoID, dest["folder_path"])
	})

	t.Run("09_clip_download_route_is_wired", func(t *testing.T) {
		// The route is POST /api/media/clips/:source/clips/:id/download. It
		// exists and is owned by the real publication handler, which fails
		// closed because this harness wires no download use case.
		rec := h.do(t, http.MethodPost, "/api/media/clips/stock/clips/e2e-clip-id/download", nil,
			map[string]string{"Idempotency-Key": "e2e-download"})
		require.NotEqual(t, http.StatusNotFound, rec.Code,
			"the canonical download route must be registered")
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "download use case not wired",
			"an unwired backend must fail closed, never fake a file")

		// The route is a write: GET must not resolve it.
		get := h.do(t, http.MethodGet, "/api/media/clips/stock/clips/e2e-clip-id/download", nil, nil)
		require.Equal(t, http.StatusNotFound, get.Code)
	})

	t.Run("10_idempotency_replay_does_not_enqueue_twice", func(t *testing.T) {
		key := "e2e-replay-key"
		payload := pipelineE2EProcessPayload(pipelineE2EGroup + "-replay")

		before := h.jobs.count()
		first := h.do(t, http.MethodPost, "/api/clips/process", payload,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusOK, first.Code, first.Body.String())
		require.Empty(t, first.Header().Get("X-Idempotency-Replay"))
		require.Equal(t, before+1, h.jobs.count())

		replay := h.do(t, http.MethodPost, "/api/clips/process", payload,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, first.Code, replay.Code)
		require.Equal(t, "true", replay.Header().Get("X-Idempotency-Replay"),
			"an identical replay must be served from the idempotency cache")
		require.Equal(t, first.Body.String(), replay.Body.String())
		require.Equal(t, before+1, h.jobs.count(),
			"a replay must never enqueue a second canonical job")

		// Same key, different body: refused (no second identity, no second job).
		other := pipelineE2EProcessPayload(pipelineE2EGroup + "-replay-conflict")
		conflict := h.do(t, http.MethodPost, "/api/clips/process", other,
			map[string]string{"Idempotency-Key": key})
		require.Equal(t, http.StatusUnprocessableEntity, conflict.Code, conflict.Body.String())
		require.Equal(t, before+1, h.jobs.count())
	})
}
