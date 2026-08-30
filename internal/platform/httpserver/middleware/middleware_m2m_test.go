// Tests for the M2M (machine-to-machine) client auth middleware.
//
// Why these tests matter: jobClientAuthMiddleware is the canonical gate
// for the public job surface (POST /api/v1/jobs, GET /api/v1/jobs/:id)
// when a second computer submits jobs to the Master. The load-bearing
// invariants pinned here:
//
//   - A valid Bearer VELOX_M2M_SECRET authenticates and stores the
//     resolved client in the gin context (so requireScope can authorize
//     the specific route's scope).
//   - A missing/wrong secret is 401 (no matching client row).
//   - A disabled client is 403 (credential valid, administratively
//     revoked — distinct from 401 so the client can tell).
//   - A store outage is 500 (fail-closed, NOT 401 — the secret is not
//     wrong, the DB is down).
//   - requireScope lets a client WITH the scope through, 403s a client
//     WITHOUT it, and passes through under EnableM2M()==false (dev/E2E).
//   - The M2M surface does NOT accept the X-Velox-Admin-Token header
//     (defense in depth: an admin token must not be replayed here).
//
// Pattern parity with admin_token_test.go (the admin-only counterpart):
// the general "auth header → 200 / no auth → 401" path is covered by the
// auth middleware tests; what this file adds is the M2M-specific
// differentiator — per-client lookup, scope enforcement, and the
// admin-header rejection that keeps the M2M principal distinct from the
// admin principal.

package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	"github.com/gin-gonic/gin"
)

// newM2MTestSecurity builds a testM2MSecurity with one registered client.
// secret is the plaintext; the fake hashes it on both insert and lookup
// so the digest round-trips exactly as in production.
func newM2MTestSecurity(enabled bool, clientID string, scopes []string, clientEnabled bool, secret string) *testM2MSecurity {
	sec := &testM2MSecurity{enabled: enabled, clients: map[string]*mw.M2MClient{}}
	hash := sec.HashClientSecret(secret)
	sec.clients[hash] = &mw.M2MClient{
		ClientID: clientID,
		Scopes:   scopes,
		Enabled:  clientEnabled,
	}
	return sec
}

// TestJobClientAuthMiddleware_ValidBearerStoresClient is the happy path:
// a valid Bearer resolves to a client, the client is stored in the
// context, and the downstream handler runs. This is the foundation the
// requireScope tests build on — if the client is not stored, every
// scope test is moot.
func TestJobClientAuthMiddleware_ValidBearerStoresClient(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "computer-editor-01",
		[]string{"jobs.submit", "jobs.read"}, true, "velox_m2m_secret_01")

	var storedClient *mw.M2MClient
	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.POST("/jobs", func(c *gin.Context) {
		raw, ok := c.Get(jobClientContextKey)
		if !ok || raw == nil {
			t.Fatal("expected m2m client stored in context")
		}
		storedClient, _ = raw.(*mw.M2MClient)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/jobs", nil)
	req.Header.Set("Authorization", "Bearer velox_m2m_secret_01")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid bearer, got %d (body=%q)", w.Code, w.Body.String())
	}
	if storedClient == nil || storedClient.ClientID != "computer-editor-01" {
		t.Fatalf("expected client_id=computer-editor-01 stored, got %+v", storedClient)
	}
}

// TestJobClientAuthMiddleware_NoBearerIs401 pins the "no credential"
// path: a missing Authorization header is 401, not 403 or 500.
func TestJobClientAuthMiddleware_NoBearerIs401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1", []string{"jobs.read"}, true, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.GET("/jobs/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/jobs/j1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing bearer, got %d", w.Code)
	}
}

// TestJobClientAuthMiddleware_WrongSecretIs401 pins the "no matching
// client row" path: a Bearer whose hash is not in the table is 401
// (indistinguishable from "no credential" so the timing does not leak
// whether a client_id exists).
func TestJobClientAuthMiddleware_WrongSecretIs401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1", []string{"jobs.read"}, true, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.GET("/jobs/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/jobs/j1", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong secret, got %d", w.Code)
	}
}

// TestJobClientAuthMiddleware_DisabledClientIs403 pins the
// "credential valid but administratively revoked" path: a disabled
// client is 403, distinct from 401 (wrong secret). This lets the
// remote computer distinguish "my secret is wrong" from "I was
// revoked" — important for operational alerting.
func TestJobClientAuthMiddleware_DisabledClientIs403(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1", []string{"jobs.read"}, false, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.GET("/jobs/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/jobs/j1", nil)
	req.Header.Set("Authorization", "Bearer s1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disabled client, got %d", w.Code)
	}
}

// TestJobClientAuthMiddleware_StoreErrorIs500 pins the fail-closed
// store-unavailable path: a LookupClient error is 500, NOT 401 — the
// secret is not wrong, the DB is down. Conflating the two would make
// the remote computer retry its secret on a DB outage.
func TestJobClientAuthMiddleware_StoreErrorIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := &testM2MSecurity{
		enabled:   true,
		clients:   map[string]*mw.M2MClient{},
		lookupErr: errors.New("db is down"),
	}

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.GET("/jobs/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/jobs/j1", nil)
	req.Header.Set("Authorization", "Bearer s1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for store error, got %d", w.Code)
	}
}

// TestJobClientAuthMiddleware_PassThroughWhenDisabled pins the
// dev/test/E2E bypass: EnableM2M()==false short-circuits to
// pass-through (admin context) without consulting the store. This
// matches Auth()'s EnableAuth bypass so existing fixtures without a
// provisioned m2m_clients row keep working until M2M is wired.
func TestJobClientAuthMiddleware_PassThroughWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := &testM2MSecurity{enabled: false, clients: map[string]*mw.M2MClient{}}

	called := false
	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.GET("/jobs/:id", func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/jobs/j1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !called {
		t.Fatalf("expected 200 pass-through when M2M disabled, got %d (called=%v)", w.Code, called)
	}
}

// TestJobClientAuthMiddleware_RejectsAdminTokenHeader is the load-bearing
// differentiator from Auth(): the X-Velox-Admin-Token header MUST NOT
// authenticate on the M2M surface. The M2M surface accepts ONLY the
// Authorization: Bearer scheme; an admin token replayed via the
// admin-only header is 401. Without this, a leaked admin token could
// submit jobs via the M2M surface, collapsing the principal boundary.
func TestJobClientAuthMiddleware_RejectsAdminTokenHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1", []string{"jobs.submit"}, true, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.POST("/jobs", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest("POST", "/jobs", nil)
	req.Header.Set("X-Velox-Admin-Token", "s1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for admin-header on M2M surface, got %d", w.Code)
	}
}

// TestRequireScope_AllowsGrantedScope pins the per-route scope gate
// happy path: a client WITH jobs.submit can POST /jobs.
func TestRequireScope_AllowsGrantedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1",
		[]string{"jobs.submit", "jobs.read"}, true, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.POST("/jobs", RequireScope("jobs.submit"), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/jobs", nil)
	req.Header.Set("Authorization", "Bearer s1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for granted scope, got %d", w.Code)
	}
}

// TestRequireScope_RejectsMissingScope pins the per-route scope gate
// 403 path: a client WITHOUT jobs.submit cannot POST /jobs, even though
// its secret is valid and the client is enabled. 403, not 401 — the
// credential is valid, the scope grant is insufficient.
func TestRequireScope_RejectsMissingScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := newM2MTestSecurity(true, "c1",
		[]string{"jobs.read"}, true, "s1")

	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.POST("/jobs", RequireScope("jobs.submit"), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/jobs", nil)
	req.Header.Set("Authorization", "Bearer s1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for missing scope, got %d", w.Code)
	}
}

// TestRequireScope_PassThroughWhenAdmin pins the dev/test/E2E bypass
// for the scope gate: when EnableM2M()==false, the auth middleware sets
// is_admin without storing a client; requireScope must then pass
// through (not 500) so fixtures without a provisioned m2m_clients row
// keep working end-to-end.
func TestRequireScope_PassThroughWhenAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := &testM2MSecurity{enabled: false, clients: map[string]*mw.M2MClient{}}

	called := false
	r := gin.New()
	r.Use(JobClientAuthMiddleware(sec, nil))
	r.POST("/jobs", RequireScope("jobs.submit"), func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/jobs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !called {
		t.Fatalf("expected 200 pass-through when M2M disabled, got %d (called=%v)", w.Code, called)
	}
}

// TestRequireScope_500WithoutAuthMiddleware pins the mis-wire path:
// requireScope mounted WITHOUT a preceding JobClientAuthMiddleware is
// 500 (fail-closed). The dev/E2E pass-through only applies when
// is_admin was explicitly set (EnableM2M()==false); a bare requireScope
// with no auth middleware at all is a wiring bug, not a pass-through.
func TestRequireScope_500WithoutAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	// NOTE: deliberately NO JobClientAuthMiddleware — simulates a
	// mis-wired route chain.
	r.POST("/jobs", RequireScope("jobs.submit"), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("POST", "/jobs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for requireScope without auth middleware, got %d", w.Code)
	}
}

// TestM2MClient_HasScope pins the scope-membership helper that the
// requireScope gate and future idempotency-key uniqueness both rely on.
func TestM2MClient_HasScope(t *testing.T) {
	c := &mw.M2MClient{ClientID: "c1", Scopes: []string{"jobs.submit", "jobs.read"}}
	if !c.HasScope("jobs.submit") {
		t.Fatal("expected jobs.submit to be granted")
	}
	if c.HasScope("admin.delete") {
		t.Fatal("expected admin.delete to NOT be granted")
	}
	if (&mw.M2MClient{}).HasScope("anything") {
		t.Fatal("empty-scope client should not match any scope")
	}
	// nil-receiver-safe (defence in depth).
	var nilClient *mw.M2MClient
	if nilClient.HasScope("jobs.submit") {
		t.Fatal("nil client should not match any scope")
	}
}
