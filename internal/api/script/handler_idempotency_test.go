package script

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestGenerate_ActiveKeyReturnsSameJobID verifies that two requests
// with different Idempotency-Keys submit independently even when the
// payload is the same.
func TestGenerate_ActiveKeyReturnsSameJobID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, _ := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", "idem-active-1")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code)

	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	jobID1 := resp1["job_id"]
	require.NotEmpty(t, jobID1)

	// Different Idempotency-Key, same payload → distinct submissions.
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "idem-active-2")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusAccepted, w2.Code)

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.NotEqual(t, jobID1, resp2["job_id"], "different Idempotency-Key should submit a distinct job")
}

func TestGenerate_IdempotencyKeyRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, _ := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// No Idempotency-Key header

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "IDEMPOTENCY_KEY_REQUIRED")
}

func TestGenerate_IdempotencyReplayReturnsSameJobID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	idemKey := "idem-replay-1"

	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code)

	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	jobID1 := resp1["job_id"]
	require.NotEmpty(t, jobID1)

	// Second request with same key and same payload should replay cached response
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusAccepted, w2.Code)
	assert.Equal(t, "true", w2.Header().Get("X-Idempotency-Replay"))

	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, jobID1, resp2["job_id"])

	// Ensure the job service was only enqueued once
	assert.Equal(t, 2, submit.submitCount, "expected two submissions with one replay for idempotent replay")
}

func TestGenerate_IdempotencyConflictDifferentPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	idemKey := "idem-conflict-1"
	body1 := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	body2 := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"different"},"script_params":{"target_words":100}}]}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)

	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code)

	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)

	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), "IDEMPOTENCY_KEY_CONFLICT")
	assert.Equal(t, 2, submit.submitCount, "expected two submissions before conflict")
}

// TestGenerate_ForceRefreshTrue_BypassesIdempotencyStore verifies that
// force_refresh=true skips the idempotency store replay and creates a
// new job even when the same Idempotency-Key already has a completed
// record.
func TestGenerate_ForceRefreshTrue_BypassesIdempotencyStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	idemKey := "idem-force-refresh-1"
	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`

	// First request: create a completed idempotency record.
	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code)

	// Second request with force_refresh=true and the same key must
	// bypass the store and enqueue a new job.
	forceBody := `{"version":2,"preset":"custom","force_refresh":true,"items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(forceBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusAccepted, w2.Code)
	assert.Equal(t, 2, submit.submitCount, "force_refresh=true should submit a new job despite existing idempotency record")
}

// TestGenerate_ForceRefreshTrue_ActiveKeyEmpty verifies that
// force_refresh=true clears the ActiveKey so the job broker cannot
// dedup against an active job.
func TestGenerate_ForceRefreshTrue_ActiveKeyEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","force_refresh":true,"items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-force-refresh-2")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.NotNil(t, submit.lastReq)
	assert.True(t, submit.lastReq.ForceRefresh, "force_refresh=true must be forwarded to submission")
}

// TestGenerate_ForceRefreshFalse_ActiveKeySet verifies the default
// path (force_refresh omitted/false) still derives and sets the
// ActiveKey for job-level dedup.
func TestGenerate_ForceRefreshFalse_ActiveKeySet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-force-refresh-3")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.NotNil(t, submit.lastReq)
	assert.Equal(t, "script.generate", submit.lastReq.JobType)
}

func TestGenerate_ActiveKeyDerivedFromFingerprint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-active-key-1")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	var env scriptpkg.GenerationEnvelopeV2
	require.NoError(t, json.Unmarshal([]byte(body), &env))
	wantPayload, err := json.Marshal(env)
	require.NoError(t, err)
	sum := sha256.Sum256(wantPayload)
	wantHash := hex.EncodeToString(sum[:])
	require.NotNil(t, submit.lastReq)
	assert.Equal(t, wantHash, submit.lastReq.RequestHash)
}
