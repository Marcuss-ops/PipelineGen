package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// researchCacheInvalidatorFake records DeleteResearchCache calls.
type researchCacheInvalidatorFake struct {
	calls      int
	lastScope  string
	lastTopic  string
	lastMetric string
	deleted    int64
	err        error
}

func (f *researchCacheInvalidatorFake) DeleteResearchCache(_ context.Context, scope, topic, metric string) (int64, error) {
	f.calls++
	f.lastScope = scope
	f.lastTopic = topic
	f.lastMetric = metric
	return f.deleted, f.err
}

func performInvalidateRequest(t *testing.T, handler *ResearchCacheInvalidateHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/cache/invalidate", handler.Invalidate)

	req := httptest.NewRequest(http.MethodPost, "/cache/invalidate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func decodeInvalidateResponse(t *testing.T, response *httptest.ResponseRecorder) ResearchCacheInvalidateResponse {
	t.Helper()
	var payload ResearchCacheInvalidateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return payload
}

func TestInvalidateRejectsInvalidScope(t *testing.T) {
	inv := &researchCacheInvalidatorFake{}
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(inv, zap.NewNop()), `{"scope":"ranking","topic":"x"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if inv.calls != 0 {
		t.Fatalf("invalid scope invoked invalidator: calls=%d", inv.calls)
	}
}

func TestInvalidateRequiresTopic(t *testing.T) {
	inv := &researchCacheInvalidatorFake{}
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(inv, zap.NewNop()), `{"scope":"aggregate"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if inv.calls != 0 {
		t.Fatalf("missing topic invoked invalidator: calls=%d", inv.calls)
	}
}

func TestInvalidateFailsClosedWhenInvalidatorMissing(t *testing.T) {
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(nil, zap.NewNop()), `{"scope":"aggregate","topic":"x"}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
}

func TestInvalidateAggregateScope(t *testing.T) {
	inv := &researchCacheInvalidatorFake{deleted: 1}
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(inv, zap.NewNop()), `{"scope":"aggregate","topic":"The richest boxers","ranking_metric":"estimated_net_worth"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if inv.calls != 1 || inv.lastScope != "aggregate" || inv.lastTopic != "The richest boxers" || inv.lastMetric != "estimated_net_worth" {
		t.Fatalf("invalidator call = %+v (calls=%d)", inv, inv.calls)
	}
	payload := decodeInvalidateResponse(t, response)
	if !payload.OK || payload.Deleted != 1 {
		t.Fatalf("response = %+v", payload)
	}
	if len(payload.Layers) != 2 || payload.Layers[0] != "aggregate" || payload.Layers[1] != "ranking" {
		t.Fatalf("layers = %v, want [aggregate ranking]", payload.Layers)
	}
}

func TestInvalidateCandidateScope(t *testing.T) {
	inv := &researchCacheInvalidatorFake{deleted: 3}
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(inv, zap.NewNop()), `{"scope":"candidate","topic":"Canelo Álvarez"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if inv.calls != 1 || inv.lastScope != "candidate" || inv.lastTopic != "Canelo Álvarez" {
		t.Fatalf("invalidator call = %+v (calls=%d)", inv, inv.calls)
	}
	payload := decodeInvalidateResponse(t, response)
	if len(payload.Layers) != 1 || payload.Layers[0] != "candidate" {
		t.Fatalf("layers = %v, want [candidate]", payload.Layers)
	}
}

func TestInvalidateSurfacesInvalidatorError(t *testing.T) {
	inv := &researchCacheInvalidatorFake{err: errors.New("db locked")}
	response := performInvalidateRequest(t, NewResearchCacheInvalidateHandler(inv, zap.NewNop()), `{"scope":"aggregate","topic":"x"}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
}
