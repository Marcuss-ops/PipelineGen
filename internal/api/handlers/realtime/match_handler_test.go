package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"velox/go-master/internal/media/realtime"
)

// mockRealtimeSvc implements realtime matching for handler tests.
type mockRealtimeSvc struct {
	matchFn func(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error)
}

func (m *mockRealtimeSvc) Match(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error) {
	return m.matchFn(ctx, req)
}

func TestMatchHandler_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := &mockRealtimeSvc{
		matchFn: func(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error) {
			assert.Equal(t, "gatto spaziale", req.Query)
			assert.Equal(t, "visual", req.Mode)
			return &realtime.MatchResponse{
				OK:        true,
				Status:    "instant_match",
				LatencyMs: 37,
				Asset: &realtime.MatchAsset{
					ID:    "artlist_001",
					Score: 0.91,
					Name:  "Space cat",
				},
			}, nil
		},
	}
	handler := NewMatchHandler(mockSvc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api")
	handler.RegisterRoutes(rg)

	body := map[string]interface{}{
		"query": "gatto spaziale",
		"mode":  "visual",
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/realtime/match", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp realtime.MatchResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "instant_match", resp.Status)
	assert.Equal(t, "artlist_001", resp.Asset.ID)
	assert.Equal(t, 37, int(resp.LatencyMs))
}

func TestMatchHandler_BadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := &mockRealtimeSvc{}
	handler := NewMatchHandler(mockSvc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api")
	handler.RegisterRoutes(rg)

	// Empty body
	req := httptest.NewRequest("POST", "/api/realtime/match", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.False(t, resp["ok"].(bool))
}

func TestMatchHandler_ServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := &mockRealtimeSvc{
		matchFn: func(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error) {
			return nil, assert.AnError
		},
	}
	handler := NewMatchHandler(mockSvc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api")
	handler.RegisterRoutes(rg)

	body := map[string]interface{}{
		"query": "test",
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/realtime/match", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestMatchHandler_FallbackGenerating(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockSvc := &mockRealtimeSvc{
		matchFn: func(ctx context.Context, req *realtime.MatchRequest) (*realtime.MatchResponse, error) {
			return &realtime.MatchResponse{
				OK:              true,
				Status:          "fallback_generating",
				LatencyMs:       42,
				GenerationJobID: "job_gen_abc",
				FallbackAsset: &realtime.MatchAsset{
					ID:    "stock_002",
					Score: 0.63,
				},
			}, nil
		},
	}
	handler := NewMatchHandler(mockSvc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api")
	handler.RegisterRoutes(rg)

	body := map[string]interface{}{
		"query":                       "forest drone shot",
		"allow_background_generation": true,
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/realtime/match", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp realtime.MatchResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, "fallback_generating", resp.Status)
	assert.Equal(t, "job_gen_abc", resp.GenerationJobID)
	assert.Equal(t, 0.63, resp.FallbackAsset.Score)
}
