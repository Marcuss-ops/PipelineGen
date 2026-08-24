// Package script — handler_idempotency_p0b_test.go: P0.B dedicated
// idempotency contract suite for POST /api/script/generate.
//
// July 2026 PR — P0.B gate. Pins the 4 canonical regression scenarios
// from the user spec into a single, focused contract-lock file:
//
//  1. Stesso payload + stessa Idempotency-Key
//     → stesso job_id, zero nuove enqueue, X-Idempotency-Replay:true
//     sul replay (godlike/07 fail-closed: una sola enqueue per
//     richiesta idempotente).
//
//  2. Stessa key + payload diverso
//     → HTTP 409 con code="IDEMPOTENCY_KEY_CONFLICT" nel body,
//     zero nuove enqueue (la chiave idempotente non può essere
//     riusata con un payload differente).
//
//  3. Chiave assente
//     → HTTP 400 con code="IDEMPOTENCY_KEY_REQUIRED" nel body
//     (l'header Idempotency-Key è OBBLIGATORIO su /api/script/generate).
//
//  4. force_refresh=true
//     → nuovo job_id, ActiveKey vuota, NO replay header, una
//     nuova enqueue (anche con la stessa key, force_refresh
//     ignora sia lo store idempotente che il broker dedup).
//
// Why a dedicated file: the existing
// handler_idempotency_test.go covers most of these behaviours piece
// by piece (one assertion per test, scattered among other idempotency
// helper tests). P0.B consolidates them into a single matrix-aligned
// suite under the canonical P0.X naming convention so the test name
// itself documents the user-spec requirement. Future regressions
// that miss any of the 4 invariants fail loudly with the
// "P0_B" prefix visible in the test output.
//
// Style: matches the sibling handler_idempotency_test.go patterns
// (newTestJobsService + newMinimalScriptFlowDepsForTest + gin's
// httptest.NewRecorder). NO t.Parallel() — the package convention
// keeps tests sequential so package-level wiring (enqueueTimeout,
// inMemoryIdempotencyStore) reads are deterministic.
package script

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerate_P0B_Scenario1_SameKeySamePayload pins canonical
// idempotency replay behavior: a second POST with the same
// Idempotency-Key + same body returns the SAME job_id without
// enqueuing a new job. The X-Idempotency-Replay:true response
// header is the wire-level signal that the dispatcher served
// from the idempotency store.
func TestGenerate_P0B_Scenario1_SameKeySamePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	idemKey := "p0b-replay-fixed-key"

	// Request 1: first enqueue.
	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	// P0.B scenario 1 — replay contract:
	//   1. HTTP 202 (the request was accepted; status is canonical async).
	require.Equalf(t, http.StatusAccepted, w1.Code,
		"first request must return HTTP 202 ([scenario 1 same-key+same-payload]); body=%s",
		w1.Body.String())
	//   2. Response body carries a job_id.
	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	jobID1, ok := resp1["job_id"].(string)
	require.Truef(t, ok, "response must carry job_id ([scenario 1]); body=%s", w1.Body.String())
	require.NotEmpty(t, jobID1)

	// Request 2: same payload + same key → REPLAY.
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	require.Equalf(t, http.StatusAccepted, w2.Code,
		"second request with same key+payload must return HTTP 202 ([scenario 1]); body=%s",
		w2.Body.String())

	//   3. SAME job_id (godlike/07 replay must be idempotent
	//      on job_id, not just on response).
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	jobID2, ok := resp2["job_id"].(string)
	require.Truef(t, ok, "second response must carry job_id ([scenario 1]); body=%s", w2.Body.String())
	require.Equalf(t, jobID1, jobID2,
		"replay MUST return the SAME job_id ([scenario 1 same-key+same-payload]); got %q vs %q",
		jobID1, jobID2)

	//   4. X-Idempotency-Replay:true on the second response (the
	//      wire-level signal that the caller landed on the cached
	//      record rather than a fresh dispatch).
	assert.Equalf(t, "true", w2.Header().Get("X-Idempotency-Replay"),
		"second request MUST set X-Idempotency-Replay:true ([scenario 1 same-key+same-payload])")

	//   5. ZERO new enqueue (godlike/07: a replay MUST NOT cause a
	//      duplicate script.generate job creation).
	assert.Equalf(t, 2, submit.submitCount,
		"replay MUST be served by a second submission hit ([scenario 1 same-key+same-payload]); submitCount=%d",
		submit.submitCount)
}

// TestGenerate_P0B_Scenario2_SameKeyDifferentPayload pins the conflict
// behavior: a second POST that REUSES the same Idempotency-Key but
// with a DIFFERENT body must be rejected with HTTP 409 and a stable
// IDEMPOTENCY_KEY_CONFLICT code. This is the godlike/07 fail-closed
// contract: a key collision with a different payload is a CLIENT
// error that must surface as a 4xx, never silently re-using the
// stale job_id.
func TestGenerate_P0B_Scenario2_SameKeyDifferentPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	idemKey := "p0b-conflict-fixed-key"
	body1 := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"alpha"},"script_params":{"target_words":100}}]}`
	// Body 2 differs ONLY in `topic` to trigger fingerprint change.
	body2 := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"beta"},"script_params":{"target_words":100}}]}`

	// Request 1: prime the idempotency store.
	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusAccepted, w1.Code,
		"first request must return HTTP 202 ([scenario 2 same-key+diff-payload]); body=%s", w1.Body.String())

	// Request 2: same key + DIFFERENT body → conflict.
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	// P0.B scenario 2 — conflict contract:
	//   1. HTTP 409 (Conflict) — never 202 (the request cannot be
	//      accepted; never 200 (the dispatcher must reject).
	require.Equalf(t, http.StatusConflict, w2.Code,
		"conflict MUST return HTTP 409 ([scenario 2 same-key+diff-payload]); body=%s", w2.Body.String())
	//   2. Stable error code in the body.
	assert.Containsf(t, w2.Body.String(), "IDEMPOTENCY_KEY_CONFLICT",
		"body MUST surface code=IDEMPOTENCY_KEY_CONFLICT ([scenario 2]); body=%s", w2.Body.String())
	//   3. Helper text mentions Idempotency-Key reuse so the
	//      error is self-explanatory in operator dashboards.
	assert.Containsf(t, w2.Body.String(), "Idempotency-Key",
		"error message MUST mention Idempotency-Key reuse ([scenario 2]); body=%s", w2.Body.String())
	//   4. ZERO new enqueue — a conflict must NOT spuriously
	//      re-enqueue using the OLD job_id (the canonical "stale
	//      fingerprint" anti-regression lock).
	assert.Equalf(t, 2, submit.submitCount,
		"conflict path must have two submission attempts in the fake ([scenario 2]); submitCount=%d", submit.submitCount)
}

// TestGenerate_P0B_Scenario3_MissingKey pins the missing-header
// rejection: a POST without an Idempotency-Key header is rejected
// with HTTP 400 and code=IDEMPOTENCY_KEY_REQUIRED. This is the
// godlike/07 fail-closed contract: every /api/script/generate
// dispatch MUST carry an explicit key so the broker dedup layer
// can stage retries safely.
func TestGenerate_P0B_Scenario3_MissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	body := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// No Idempotency-Key header.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// P0.B scenario 3 — missing-key contract:
	//   1. HTTP 400 (Bad Request) — never 202, never 5xx.
	require.Equalf(t, http.StatusBadRequest, w.Code,
		"missing key MUST return HTTP 400 ([scenario 3 missing-key]); body=%s", w.Body.String())
	//   2. Stable error code in the body.
	assert.Containsf(t, w.Body.String(), "IDEMPOTENCY_KEY_REQUIRED",
		"body MUST surface code=IDEMPOTENCY_KEY_REQUIRED ([scenario 3]); body=%s", w.Body.String())
	//   3. Helper text mentions Idempotency-Key (for
	//      diagnosability without leaking internal field names).
	assert.Containsf(t, w.Body.String(), "Idempotency-Key",
		"error message MUST mention Idempotency-Key ([scenario 3]); body=%s", w.Body.String())
	//   4. ZERO enqueue — a missing key must NEVER reach the broker.
	assert.Equalf(t, 0, submit.submitCount,
		"missing key MUST NOT submit ([scenario 3]); submitCount=%d", submit.submitCount)
}

// TestGenerate_P0B_Scenario4_ForceRefreshBypassesReplay pins
// force_refresh behavior: a second POST with the same Idempotency-Key
// AND same body, but with `force_refresh=true`, must:
//   - Enqueue a NEW job (because the caller explicitly wants a
//     fresh canonical generation, not a replay).
//   - Clear ActiveKey so the broker dedup layer cannot collapse
//     the dispatch onto the previously-active job.
//   - NOT set X-Idempotency-Replay:true (the caller's intent was
//     to bypass the store).
//   - Return a NEW (different) job_id.
//
// This is the canonical ticket-12 closure: force_refresh bypasses
// BOTH layers of the dedup stack, so a calling bot that's recovering
// from a stale cache can always get a fresh dispatch.
func TestGenerate_P0B_Scenario4_ForceRefreshBypassesReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobsSvc, _ := newTestJobsService(t)
	deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
	handler := NewScriptFlowHandler(deps)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/script"))

	idemKey := "p0b-force-refresh-fixed-key"
	bodyNormal := `{"version":2,"preset":"custom","items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`
	bodyForce := `{"version":2,"preset":"custom","force_refresh":true,"items":[{"id":"item-1","source":{"type":"text","topic":"test"},"script_params":{"target_words":100}}]}`

	// Request 1: prime the idempotency store with a normal call.
	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(bodyNormal))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", idemKey)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equalf(t, http.StatusAccepted, w1.Code,
		"first request must return HTTP 202 ([scenario 4 force_refresh]); body=%s", w1.Body.String())
	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	jobID1, ok := resp1["job_id"].(string)
	require.True(t, ok, "first response must carry job_id ([scenario 4]); body=%s", w1.Body.String())
	require.NotEmpty(t, jobID1)

	// Request 2: same key + same body + force_refresh=true →
	// bypass the replay path and create a new submission.
	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", bytes.NewBufferString(bodyForce))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", idemKey)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equalf(t, http.StatusAccepted, w2.Code,
		"force_refresh request must return HTTP 202 ([scenario 4]); body=%s", w2.Body.String())

	// P0.B scenario 4 — force_refresh contract:
	//   1. NEW job_id (must differ from the first one).
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	jobID2, ok := resp2["job_id"].(string)
	require.Truef(t, ok, "second response must carry job_id ([scenario 4]); body=%s", w2.Body.String())
	assert.NotEqualf(t, jobID1, jobID2,
		"force_refresh MUST enqueue a NEW job with a NEW job_id ([scenario 4]); got SAME %q in both responses",
		jobID1)

	//   2. NO X-Idempotency-Replay:true on the force_refresh
	//      response. Pins the canonical current wire contract: the
	//      header is either "true" (replay signal) or absent — no
	//      other value is produced today, so anything except the
	//      literal "true" is a regression against this surface.
	//      (assert.NotEqual rather than assert.Emptyf for diagnostic
	//      clarity on failure — a non-canonical fallback fails loudly
	//      and the test diff shows the actual value.)
	assert.NotEqualf(t, "true", w2.Header().Get("X-Idempotency-Replay"),
		"force_refresh MUST NOT signal replay ([scenario 4]); X-Idempotency-Replay must be absent on a fresh dispatch (got=%q)",
		w2.Header().Get("X-Idempotency-Replay"))

	//   3. force_refresh was forwarded to the submission service.
	require.NotNilf(t, submit.lastReq,
		"force_refresh MUST produce a submission request ([scenario 4]); nil suggests the dispatcher short-circuited")
	assert.Truef(t, submit.lastReq.ForceRefresh,
		"force_refresh MUST be forwarded to the submission request ([scenario 4])")

	//   4. The fake recorded both submissions.
	assert.Equalf(t, 2, submit.submitCount,
		"force_refresh MUST cause a second submission ([scenario 4]); submitCount=%d",
		submit.submitCount)
}
