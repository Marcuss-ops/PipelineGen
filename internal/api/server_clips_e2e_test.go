package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	cliphttp "github.com/Marcuss-ops/PipelineGen/internal/api/assets/youtube"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	yttypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type serverE2EYouTubeService struct{}

func (s *serverE2EYouTubeService) Config() yttypes.RuntimeConfig {
	return yttypes.RuntimeConfig{}
}

func (s *serverE2EYouTubeService) GetVideoInfo(_ context.Context, _ string) (*ytports.DownloaderMetadata, error) {
	return &ytports.DownloaderMetadata{}, nil
}

func (s *serverE2EYouTubeService) SearchByTopicWithFilter(_ context.Context, _ string, _ int, _, _ string) (*youtube.TopicSearchResponse, error) {
	return &youtube.TopicSearchResponse{OK: true}, nil
}

func (s *serverE2EYouTubeService) Extract(_ context.Context, _ *yttypes.ExtractRequest) (*yttypes.ExtractResponse, error) {
	return &yttypes.ExtractResponse{}, nil
}

func (s *serverE2EYouTubeService) GetOrCreateChannelFolder(_ context.Context, channelName, parentFolderID string) (string, error) {
	return filepath.Join(parentFolderID, channelName), nil
}

type serverE2EJobsService struct {
	lastReq *job.EnqueueRequest
}

func (s *serverE2EJobsService) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.lastReq = req
	return &job.Job{ID: "job-123"}, nil
}

func (s *serverE2EJobsService) Get(context.Context, string) (*job.Job, error) { return nil, nil }
func (s *serverE2EJobsService) Cancel(context.Context, string) error          { return nil }
func (s *serverE2EJobsService) List(context.Context, job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *serverE2EJobsService) IsTerminal(status job.Status) bool { return status.IsTerminal() }
func (s *serverE2EJobsService) RegisterHandler(string, any) error { return nil }
func (s *serverE2EJobsService) ListEvents(context.Context, string) ([]job.Event, error) {
	return nil, nil
}
func (s *serverE2EJobsService) Retry(context.Context, string) (*job.Job, error) { return nil, nil }

// mockAIStockClipsHandler is a minimal stand-in for the clips module that
// only registers the AI stock ingestion route. It lets the end-to-end
// router test verify the full path /api/clips/ingest/ai-stock without
// wiring the real clips module and its heavy dependencies.
type mockAIStockClipsHandler struct{}

func (m *mockAIStockClipsHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/ingest/ai-stock", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "clip_id": "mock-ai-stock-01"})
	})
}

// mockLegacyYouTubeClipsHandler is a minimal stand-in for the legacy
// YouTube clip handler that mounts under /api/clips/*. It lets the
// end-to-end router test verify the capability wire surface without
// wiring the real YouTube service and its dependencies.
type mockLegacyYouTubeClipsHandler struct{}

func (m *mockLegacyYouTubeClipsHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/process", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "job_id": "mock-job-01"})
	})
}

func TestNewServerWithHealth_AIStockClipRoutesThroughRealRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"clips",
		func() bool { return true },
		"/clips",
		&mockAIStockClipsHandler{},
		zap.NewNop(),
	)))

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

	body := map[string]any{
		"document": map[string]any{
			"asset": map[string]any{
				"proposed_asset_id": "mock-ai-stock-01",
				"title":             "Mock AI stock clip",
			},
		},
		"drive_url": "https://drive.google.com/file/d/MOCK123/view",
	}

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/clips/ingest/ai-stock", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp["ok"].(bool))
	require.Equal(t, "mock-ai-stock-01", resp["clip_id"])
}

func TestNewServerWithHealth_AIStockClipRoutesThroughAssetsModule(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	mockClips := api.NewRouteModule(
		"clips",
		func() bool { return true },
		"/clips",
		&mockAIStockClipsHandler{},
		zap.NewNop(),
	)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Clips: mockClips,
	}, zap.NewNop())

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		zap.NewNop(),
	)))

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

	body := map[string]any{
		"document": map[string]any{
			"asset": map[string]any{
				"proposed_asset_id": "mock-ai-stock-01",
				"title":             "Mock AI stock clip",
			},
		},
		"drive_url": "https://drive.google.com/file/d/MOCK123/view",
	}

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/media/clips/ingest/ai-stock", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp["ok"].(bool))
	require.Equal(t, "mock-ai-stock-01", resp["clip_id"])
}

func TestNewServerWithHealth_AssetsModuleReportsClipsCapabilityMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	mockClips := api.NewRouteModule(
		"clips",
		func() bool { return true },
		"/clips",
		&mockAIStockClipsHandler{},
		zap.NewNop(),
	)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Clips: mockClips,
	}, zap.NewNop())

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		zap.NewNop(),
	)))

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	caps, ok := resp["capabilities"].(map[string]any)
	require.True(t, ok, "capabilities must be present in response")
	require.Equal(t, "MOUNTED", caps["clips"], "clips capability must be reported as MOUNTED")
}

func TestNewServerWithHealth_LegacyYouTubeModuleReportsYouTubeCapabilityMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"youtube",
		func() bool { return true },
		"/clips",
		&mockLegacyYouTubeClipsHandler{},
		zap.NewNop(),
	)))

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp["ok"].(bool), "capabilities response must report ok=true")

	caps, ok := resp["capabilities"].(map[string]any)
	require.True(t, ok, "capabilities must be present in response")
	require.Equal(t, "MOUNTED", caps["youtube"], "youtube capability must be reported as MOUNTED")
	require.Equal(t, "NOT_MOUNTED", caps["clips"], "clips capability must be NOT_MOUNTED when only legacy /api/clips/* routes are registered")
}

// mockHealthChecker provides trivial passing checks for the /ready E2E test.
type mockHealthChecker struct{}

func (mockHealthChecker) CheckDB(context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "duration_ms": int64(1)}
}
func (mockHealthChecker) CheckDrive(context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "applicable": false, "duration_ms": int64(1)}
}
func (mockHealthChecker) CheckQdrant(context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "applicable": false, "duration_ms": int64(1)}
}
func (mockHealthChecker) CheckJobs(context.Context) systemhealth.CheckResult {
	return systemhealth.CheckResult{"ok": true, "duration_ms": int64(1)}
}

func TestNewServerWithHealth_ReadyWireReportsClipsMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	mockClips := api.NewRouteModule(
		"clips",
		func() bool { return true },
		"/clips",
		&mockAIStockClipsHandler{},
		zap.NewNop(),
	)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Clips: mockClips,
	}, zap.NewNop())

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		zap.NewNop(),
	)))

	healthSvc := systemhealth.NewService(systemhealth.ServiceDeps{
		DB:     mockHealthChecker{},
		Drive:  mockHealthChecker{},
		Qdrant: mockHealthChecker{},
		Jobs:   mockHealthChecker{},
	})
	readyChecker := systemhealth.NewReadyChecker(healthSvc)

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
		Health:   healthSvc,
		Ready:    readyChecker,
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp["ok"].(bool), "ready response must report ok=true")

	wire, ok := resp["wire"].(map[string]any)
	require.True(t, ok, "wire must be present in response")
	require.Equal(t, "MOUNTED", wire["clips"], "clips must be reported as MOUNTED in /ready wire")
}

func TestNewServerWithHealth_NoClipsModuleReportsClipsCapabilityNotMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	registry := api.NewRegistry()

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rec := httptest.NewRecorder()

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp["ok"].(bool), "capabilities response must report ok=true")

	caps, ok := resp["capabilities"].(map[string]any)
	require.True(t, ok, "capabilities must be present in response")
	require.Equal(t, "NOT_MOUNTED", caps["clips"], "clips capability must be reported as NOT_MOUNTED when no clips module is registered")
	require.Equal(t, "NOT_MOUNTED", caps["youtube"], "youtube capability must be reported as NOT_MOUNTED when no legacy /api/clips module is registered")
}

func TestNewServerWithHealth_YouTubeClipsProcessRoutesThroughRealRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	downloadDir := filepath.Join(dataDir, "downloads")

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0,
			GinMode:      gin.TestMode,
			ReadTimeout:  1,
			WriteTimeout: 1,
		},
		Storage: config.StorageConfig{
			DataDir: dataDir,
		},
		Security: config.SecurityConfig{
			EnableAuth:       false,
			RateLimitEnabled: false,
		},
		GoogleAccounting: config.GoogleAccountingConfig{
			DownloadDir: downloadDir,
		},
	}

	jobsSvc := &serverE2EJobsService{}
	handler := cliphttp.NewYouTubeClipHandler(
		&serverE2EYouTubeService{},
		zap.NewNop(),
		jobsSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	registry := api.NewRegistry()
	require.NoError(t, registry.Register(api.NewRouteModule(
		"clips",
		func() bool { return true },
		"/clips",
		handler,
		zap.NewNop(),
	)))

	server := api.NewServerWithHealth(api.ServerDeps{
		Config:   cfg,
		Registry: registry,
	})

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

	server.GetRouter().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, jobsSvc.lastReq)
	require.Equal(t, appjobs.TypeYouTubeClipExtract, jobsSvc.lastReq.Type)

	payload, ok := jobsSvc.lastReq.Payload.(map[string]any)
	require.True(t, ok, "payload must be a JSON object map")

	segments, ok := payload["segments"].([]any)
	require.True(t, ok, "segments must be an array")
	require.Len(t, segments, 3)

	dest, ok := payload["destination"].(map[string]any)
	require.True(t, ok, "destination must be present in payload")
	require.Equal(t, "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ", dest["folder_id"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["group"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["folder_path"])
	require.Equal(t, "Manny Pacquiao vs Adrien Broner", dest["subfolder_name"])
	require.Equal(t, true, dest["create_subfolder"])
	require.Equal(t, "verify", payload["strategy"])
}
