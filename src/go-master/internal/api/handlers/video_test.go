package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/pipeline"
)

// ---------------------------------------------------------------------------
// Mock implementations for pipeline dependencies
// ---------------------------------------------------------------------------

// mockScriptGenerator implements pipeline.ScriptGenerator
type mockScriptGenerator struct {
	generateFn func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error)
}

func (m *mockScriptGenerator) GenerateFromText(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, req)
	}
	return &ollama.GenerationResult{
		Script:      "Generated script",
		WordCount:   100,
		EstDuration: 30,
		Model:       "llama2",
	}, nil
}

// mockEntityService implements pipeline.EntityService
type mockEntityService struct {
	analyzeFn func(ctx context.Context, script string, entityCount int, cfg entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error)
	segmenterFn func() entities.Segmenter
}

func (m *mockEntityService) AnalyzeScript(ctx context.Context, script string, entityCount int, cfg entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
	if m.analyzeFn != nil {
		return m.analyzeFn(ctx, script, entityCount, cfg)
	}
	return &entities.ScriptEntityAnalysis{
		TotalSegments:         2,
		SegmentEntities:       []entities.SegmentEntityResult{},
		TotalEntities:         5,
		EntityCountPerSegment: 10,
	}, nil
}

func (m *mockEntityService) Segmenter() entities.Segmenter {
	if m.segmenterFn != nil {
		return m.segmenterFn()
	}
	return &mockSegmenter{}
}

type mockSegmenter struct {
	countWordsFn func(text string) int
}

func (m *mockSegmenter) Split(text string, cfg entities.SegmentConfig) []string { return []string{text} }
func (m *mockSegmenter) CountWords(text string) int {
	if m.countWordsFn != nil {
		return m.countWordsFn(text)
	}
	return len(text) / 5
}
func (m *mockSegmenter) EstimateSegments(text string, wordsPerSegment int) int { return 1 }

// mockTTSGenerator implements pipeline.TTSGenerator
type mockTTSGenerator struct {
	generateFn func(ctx context.Context, text string, language string) (*pipeline.TTSResult, error)
}

func (m *mockTTSGenerator) Generate(ctx context.Context, text string, language string) (*pipeline.TTSResult, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, text, language)
	}
	return &pipeline.TTSResult{
		FilePath:  "/tmp/audio.mp3",
		Duration:  30.0,
		Language:  language,
		VoiceUsed: "default-voice",
	}, nil
}

// mockVideoProcessor implements pipeline.VideoProcessor
type mockVideoProcessor struct {
	generateFn func(ctx context.Context, req pipeline.VideoGenerationRequest) (*pipeline.VideoGenerationResult, error)
}

func (m *mockVideoProcessor) GenerateVideo(ctx context.Context, req pipeline.VideoGenerationRequest) (*pipeline.VideoGenerationResult, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, req)
	}
	return &pipeline.VideoGenerationResult{
		JobID:     req.JobID,
		VideoPath: req.OutputPath,
		Status:    "created",
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestVideoHandlerService(opts ...func(*mockScriptGenerator, *mockEntityService, *mockTTSGenerator, *mockVideoProcessor)) *pipeline.VideoCreationService {
	ms := &mockScriptGenerator{}
	me := &mockEntityService{}
	mt := &mockTTSGenerator{}
	mv := &mockVideoProcessor{}
	if opts != nil {
		for _, opt := range opts {
			if opt != nil {
				opt(ms, me, mt, mv)
			}
		}
	}
	return pipeline.NewVideoCreationService(ms, me, mt, mv)
}

func setupVideoTestRouter(svc *pipeline.VideoCreationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// RegisterRoutes creates a "/video" group internally, so we mount at "/api"
	apiGroup := router.Group("/api")

	handler, err := NewVideoHandler(svc)
	if err != nil {
		panic(err)
	}
	handler.RegisterRoutes(apiGroup)

	return router
}

// ---------------------------------------------------------------------------
// Tests: POST /api/video/create-master
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster(t *testing.T) {
	tests := []struct {
		name           string
		payload        interface{}
		setupMocks     func(*mockScriptGenerator, *mockEntityService, *mockTTSGenerator, *mockVideoProcessor)
		expectedStatus int
		checkResponse  func(t *testing.T, body map[string]interface{})
	}{
		{
			name: "valid request with all fields",
			payload: CreateMasterRequest{
				VideoName:   "Test Video",
				ProjectName: "test-project",
				ScriptText:  "Test script text",
				Language:    "en",
				Duration:    60,
				DriveFolder: "drive-folder-123",
				SkipGDocs:   true,
				EntityCount: 10,
			},
			expectedStatus: http.StatusAccepted,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.True(t, body["ok"].(bool))
				assert.NotEmpty(t, body["job_id"])
				assert.Equal(t, "Test Video", body["video_name"])
				assert.Equal(t, "test-project", body["project_name"])
				assert.Equal(t, "processing", body["status"])
			},
		},
		{
			name: "valid request minimal fields",
			payload: CreateMasterRequest{
				VideoName:  "Minimal Video",
				ScriptText: "a script",
				Duration:   30,
				SkipGDocs:  true,
			},
			expectedStatus: http.StatusAccepted,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.True(t, body["ok"].(bool))
				assert.Equal(t, "Minimal Video", body["video_name"])
			},
		},
		{
			name: "missing required video_name",
			payload: CreateMasterRequest{
				ProjectName: "test-project",
				Duration:    30,
				SkipGDocs:   true,
			},
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				errMsg, ok := body["error"].(string)
				require.True(t, ok)
				// Gin validator reports the Go field name, not the JSON tag
				assert.Contains(t, errMsg, "VideoName")
			},
		},
		{
			name:           "invalid JSON - empty body",
			payload:        nil,
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				_, hasError := body["error"]
				assert.True(t, hasError)
			},
		},
		{
			name:           "invalid JSON - malformed",
			payload:        "not-a-json-object",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
			},
		},
		{
			name: "pipeline service validation error - no source",
			payload: CreateMasterRequest{
				VideoName: "Error Video",
				Duration:  30,
				// No ScriptText, Source, or YouTubeURL
			},
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				assert.Contains(t, body["error"], "Video creation failed")
			},
		},
		{
			name: "pipeline service validation error - duration too short",
			payload: CreateMasterRequest{
				VideoName:  "Error Video",
				ScriptText: "script",
				Duration:   5,
			},
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				assert.Contains(t, body["error"], "Video creation failed")
			},
		},
		{
			name: "script generation failure",
			payload: CreateMasterRequest{
				VideoName: "Script Error Video",
				Source:    "source text",
				Language:  "en",
				Duration:  30,
			},
			setupMocks: func(ms *mockScriptGenerator, me *mockEntityService, mt *mockTTSGenerator, mv *mockVideoProcessor) {
				ms.generateFn = func(ctx context.Context, req *ollama.TextGenerationRequest) (*ollama.GenerationResult, error) {
					return nil, errors.New("ollama connection failed")
				}
			},
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				assert.Contains(t, body["error"], "Video creation failed")
				assert.Contains(t, body["error"], "script generation failed")
			},
		},
		{
			name: "entity extraction failure is non-fatal",
			payload: CreateMasterRequest{
				VideoName:  "Entity Error Video",
				ScriptText: "some script",
				Duration:   30,
				SkipGDocs:  true,
			},
			setupMocks: func(ms *mockScriptGenerator, me *mockEntityService, mt *mockTTSGenerator, mv *mockVideoProcessor) {
				me.analyzeFn = func(ctx context.Context, script string, entityCount int, cfg entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
					return nil, errors.New("analysis failed")
				}
			},
			expectedStatus: http.StatusAccepted,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.True(t, body["ok"].(bool))
				// Pipeline continues despite entity extraction failure
			},
		},
		{
			name: "voiceover generation failure stops pipeline",
			payload: CreateMasterRequest{
				VideoName:  "Voiceover Error Video",
				ScriptText: "some script",
				Language:   "en",
				Duration:   30,
				SkipGDocs:  false,
			},
			setupMocks: func(ms *mockScriptGenerator, me *mockEntityService, mt *mockTTSGenerator, mv *mockVideoProcessor) {
				mt.generateFn = func(ctx context.Context, text string, language string) (*pipeline.TTSResult, error) {
					return nil, errors.New("TTS failure")
				}
			},
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body map[string]interface{}) {
				t.Helper()
				assert.False(t, body["ok"].(bool))
				assert.Contains(t, body["error"], "Video creation failed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVideoHandlerService(tt.setupMocks)
			router := setupVideoTestRouter(svc)

			var body *bytes.Buffer
			if tt.payload != nil {
				jsonPayload, err := json.Marshal(tt.payload)
				require.NoError(t, err)
				body = bytes.NewBuffer(jsonPayload)
			} else {
				body = bytes.NewBuffer([]byte{})
			}

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/video/create-master", body)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.checkResponse != nil {
				tt.checkResponse(t, response)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /api/video/generate and /api/video/process
// ---------------------------------------------------------------------------

// Note: The current video.go handler does not expose /api/video/generate or
// /api/video/process endpoints directly. Those routes are documented in the
// API docs but are handled by other handler files (stock_process.go, etc.)
// or delegated through the CreateMaster pipeline. The video.go handler only
// registers /create-master, /health, and /info.

// ---------------------------------------------------------------------------
// Tests: GET /api/video/status/:id
// ---------------------------------------------------------------------------

// Note: The video.go handler does not expose /api/video/status/:id. Status
// information is returned as part of the CreateMaster response. Job status
// queries would go through the job handler endpoints.

// ---------------------------------------------------------------------------
// Tests: GET /api/video/health
// ---------------------------------------------------------------------------

func TestVideoHandler_Health(t *testing.T) {
	svc := newTestVideoHandlerService()
	router := setupVideoTestRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/video/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["ok"].(bool))
	assert.Equal(t, "healthy", response["status"])
	assert.Equal(t, "video-processor", response["service"])
}

// ---------------------------------------------------------------------------
// Tests: GET /api/video/info
// ---------------------------------------------------------------------------

func TestVideoHandler_GetInfo(t *testing.T) {
	svc := newTestVideoHandlerService()
	router := setupVideoTestRouter(svc)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/video/info", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["ok"].(bool))

	service, ok := response["service"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "velox-video-processor", service["name"])
	assert.Equal(t, "1.0.0-go", service["version"])
	assert.Equal(t, "rust", service["backend"])

	capabilities, ok := response["capabilities"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, capabilities)

	endpoints, ok := response["endpoints"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, endpoints)
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster - response structure validation
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster_ResponseStructure(t *testing.T) {
	svc := newTestVideoHandlerService(func(ms *mockScriptGenerator, me *mockEntityService, mt *mockTTSGenerator, mv *mockVideoProcessor) {
		me.analyzeFn = func(ctx context.Context, script string, entityCount int, cfg entities.SegmentConfig) (*entities.ScriptEntityAnalysis, error) {
			return &entities.ScriptEntityAnalysis{
				TotalSegments:         3,
				SegmentEntities:       []entities.SegmentEntityResult{},
				TotalEntities:         15,
				EntityCountPerSegment: 5,
			}, nil
		}
		mv.generateFn = func(ctx context.Context, req pipeline.VideoGenerationRequest) (*pipeline.VideoGenerationResult, error) {
			return &pipeline.VideoGenerationResult{
				JobID:     req.JobID,
				VideoPath: "/tmp/videos/test.mp4",
				Status:    "created",
			}, nil
		}
	})

	router := setupVideoTestRouter(svc)

	payload := CreateMasterRequest{
		VideoName:   "Test Video",
		Source:      "source text here",
		Language:    "en",
		Duration:    60,
		EntityCount: 5,
	}
	jsonPayload, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/video/create-master", bytes.NewBuffer(jsonPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Validate top-level fields
	assert.True(t, response["ok"].(bool))
	assert.NotEmpty(t, response["job_id"])
	assert.Equal(t, "Test Video", response["video_name"])
	assert.Equal(t, "processing", response["status"])

	// Validate script section
	script, ok := response["script"].(map[string]interface{})
	require.True(t, ok)
	assert.True(t, script["generated"].(bool))
	assert.NotEmpty(t, script["script_text"])
	assert.NotZero(t, script["word_count"])

	// Validate entities section
	entitiesMap, ok := response["entities"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(3), entitiesMap["total_segments"])
	assert.Equal(t, float64(15), entitiesMap["total_entities"])

	// Validate voiceover section
	voiceover, ok := response["voiceover"].(map[string]interface{})
	require.True(t, ok)
	assert.True(t, voiceover["generated"].(bool))

	// Validate video section
	video, ok := response["video"].(map[string]interface{})
	require.True(t, ok)
	assert.True(t, video["created"].(bool))
	assert.Equal(t, "/tmp/videos/test.mp4", video["output"])
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster - various language codes
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster_LanguageCodes(t *testing.T) {
	languageTests := []struct {
		name     string
		language string
	}{
		{"Italian code", "it"},
		{"English code", "en"},
		{"Spanish code", "es"},
		{"French code", "fr"},
		{"German code", "de"},
		{"Empty language defaults", ""},
	}

	for _, tt := range languageTests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVideoHandlerService()
			router := setupVideoTestRouter(svc)

			payload := CreateMasterRequest{
				VideoName:  "Language Test",
				ScriptText: "test script",
				Language:   tt.language,
				Duration:   30,
				SkipGDocs:  true,
			}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/video/create-master", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusAccepted, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.True(t, response["ok"].(bool))
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster - SkipGDocs flag
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster_SkipGDocs(t *testing.T) {
	tests := []struct {
		name      string
		skipGDocs bool
	}{
		{"skip gdocs true", true},
		{"skip gdocs false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestVideoHandlerService()
			router := setupVideoTestRouter(svc)

			payload := CreateMasterRequest{
				VideoName:  "Test",
				ScriptText: "script",
				Duration:   30,
				SkipGDocs:  tt.skipGDocs,
			}
			jsonPayload, _ := json.Marshal(payload)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/api/video/create-master", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusAccepted, w.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster - concurrent requests
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster_ConcurrentRequests(t *testing.T) {
	svc := newTestVideoHandlerService()
	router := setupVideoTestRouter(svc)

	requests := []CreateMasterRequest{
		{VideoName: "Video 1", ScriptText: "script 1", Duration: 30, SkipGDocs: true},
		{VideoName: "Video 2", ScriptText: "script 2", Duration: 60, SkipGDocs: true},
		{VideoName: "Video 3", ScriptText: "script 3", Duration: 45, SkipGDocs: true},
	}

	for _, req := range requests {
		t.Run(req.VideoName, func(t *testing.T) {
			jsonPayload, _ := json.Marshal(req)

			w := httptest.NewRecorder()
			httpReq, _ := http.NewRequest("POST", "/api/video/create-master", bytes.NewBuffer(jsonPayload))
			httpReq.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, httpReq)

			assert.Equal(t, http.StatusAccepted, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.True(t, response["ok"].(bool))
			assert.Equal(t, req.VideoName, response["video_name"])
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: CreateMaster - video processor failure is non-fatal
// ---------------------------------------------------------------------------

func TestVideoHandler_CreateMaster_VideoProcessorFailure(t *testing.T) {
	svc := newTestVideoHandlerService(func(ms *mockScriptGenerator, me *mockEntityService, mt *mockTTSGenerator, mv *mockVideoProcessor) {
		mv.generateFn = func(ctx context.Context, req pipeline.VideoGenerationRequest) (*pipeline.VideoGenerationResult, error) {
			return nil, errors.New("rust binary failed")
		}
	})

	router := setupVideoTestRouter(svc)

	payload := CreateMasterRequest{
		VideoName:  "Video Proc Fail",
		ScriptText: "script",
		Duration:   30,
		SkipGDocs:  true,
	}
	jsonPayload, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/video/create-master", bytes.NewBuffer(jsonPayload))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// Video processor failure is non-fatal in the pipeline
	assert.Equal(t, http.StatusAccepted, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.True(t, response["ok"].(bool))
}
