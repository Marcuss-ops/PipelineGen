// WorkerAuth tests — PR-B locking
//
// WorkerAuth is mounted on /internal/v1/* to gate the remote-worker's
// claim / heartbeat / asset-transfer endpoints. The invariant we lock
// here:
//
//   1. A missing credential returns 401 (no scrimmage path).
//   2. The admin token is REJECTED on /internal/v1 even though Auth()
//      would accept it on /api/* — this is the defense-in-depth claim
//      of PR-B. If a leaked admin token could claim jobs, the
//      separation of server and worker would be cosmetic.
//   3. A wrong worker token returns 401.
//   4. A correct worker token (sent via either X-Velox-Admin-Token
//      OR Authorization: Bearer — both are accepted for ergonomics)
//      returns 200.
//   5. The token VALUE never leaks into the response body — leaks
//      would show up here as response-body matches.
//
// A bonus assertion (separate test) checks that an empty
// WorkerToken configuration refuses to serve — a misconfigured
// production server must fail loud, not silently open the door.
//
// PG-006 (June 2026): the previous tests constructed
// `&config.Config{Security: config.SecurityConfig{...}}` literals to
// drive WorkerAuth. With the typed-port cascade those imports are gone
// from this package; the testSecurity stub from port_fakes_test.go
// (a 3-method AuthSecurityPort fake) replaces them.

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestWorkerAuth_RejectsMissingToken — sanity baseline.
func TestWorkerAuth_RejectsMissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sec := &testSecurity{enabled: true, worker: "worker-secret-DO-NOT-LEAK"}
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWorkerAuth_RejectsAdminToken — the load-bearing defense-in-depth
// claim. If this ever returns 200, the worker/admin separation is
// cosmetic and a leaked admin token CAN claim jobs remotely.
func TestWorkerAuth_RejectsAdminToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const adminToken = "admin-secret-DO-NOT-LEAK"
	const workerToken = "worker-secret-DO-NOT-LEAK"
	sec := &testSecurity{enabled: true, admin: adminToken, worker: workerToken}
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Admin token via X-Velox-Admin-Token → MUST be rejected.
	req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("X-Velox-Admin-Token", adminToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code,
		"admin token leaked into /internal/v1 — defense-in-depth broken")

	// Admin token via Authorization: Bearer → MUST be rejected too,
	// same reasoning.
	req = httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code,
		"admin bearer token leaked into /internal/v1 — defense-in-depth broken")
}

// TestWorkerAuth_RejectsWrongWorkerToken — wrong value must 401.
func TestWorkerAuth_RejectsWrongWorkerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sec := &testSecurity{enabled: true, worker: "right-worker-secret"}
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestWorkerAuth_AcceptsCorrectWorkerToken — happy path. Both header
// channels are accepted so the remote worker (which sends
// Authorization: Bearer via jobbrokerclient) and any future
// X-Velox-Admin-Token-based diagnostic tooling can both authenticate.
func TestWorkerAuth_AcceptsCorrectWorkerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const workerToken = "right-worker-secret-DO-NOT-LEAK"
	sec := &testSecurity{enabled: true, worker: workerToken}
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	t.Run("Authorization Bearer", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
		req.Header.Set("Authorization", "Bearer "+workerToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("X-Velox-Admin-Token header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
		req.Header.Set("X-Velox-Admin-Token", workerToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})
}

// TestWorkerAuth_DoesNotLeakTokenInResponse — the misconfig-leak
// defensive test. If a future contributor accidentally echoes the
// token back to the caller, the test catches it via a substring match.
func TestWorkerAuth_DoesNotLeakTokenInResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const workerToken = "very-secret-DO-NOT-LEAK-IN-RESPONSE"
	sec := &testSecurity{enabled: true, worker: workerToken}
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Wrong token attempt — body must not contain the real secret.
	req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, strings.Contains(w.Body.String(), workerToken),
		"rejected response leaked the configured worker token")

	// Correct token — body must not echo it either.
	req = httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("Authorization", "Bearer "+workerToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, strings.Contains(w.Body.String(), workerToken),
		"successful response echoed the worker token")
}

// TestWorkerAuth_RefusesEmptyWorkerToken — fail-closed when misconfigured.
// A production server with VELOX_WORKER_TOKEN unset (e.g. operator
// typo'd the env var name) MUST NOT serve /internal/v1/* requests
// as if auth were optional; that would be an open door. We refuse
// the request with a 500 and a clear message so the operator notices.
//
// PG-006 (June 2026): the previous implementation returned 500 from a
// `cfg == nil` branch in the WorkerAuth constructor. With the typed
// port cascade, that branch lives in `sec == nil` or `sec.WorkerToken()
// == ""`. Whichever defaults the port to empty token, the production
// middleware returns 500 with a clear env-var hint.
func TestWorkerAuth_RefusesEmptyWorkerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sec := &testSecurity{enabled: true, worker: ""} // intentionally empty
	r := gin.New()
	r.Use(WorkerAuth(sec, nil))
	r.POST("/internal/v1/jobs/claim", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Even a token that would normally match the empty string MUST NOT
	// gain access — the misconfig guard rejects everything.
	req := httptest.NewRequest("POST", "/internal/v1/jobs/claim", nil)
	req.Header.Set("Authorization", "Bearer some-credential")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"empty WorkerToken should refuse (not silently allow) requests")
	require.Contains(t, w.Body.String(), "VELOX_WORKER_TOKEN",
		"misconfig error message should point operator at the env var")
}
