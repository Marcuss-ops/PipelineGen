// Tests for the admin-only middleware.
//
// Why these tests matter: RequireAdminToken is the canonical way to
// gate the job-status lookup endpoints (handler_job_status.go::PR4.F3)
// and any future admin-restricted endpoint. A regression in the
// middleware that lets a worker token slip through would silently
// widen the privilege boundary between admin and worker — exactly the
// class of bug that the audit's P0 #2 closed for the webhook path.
//
// The general "auth header → 200 / no auth → 401" path is already
// covered by TestAuth_RejectsQueryStringToken_EndToEnd and
// TestAuth_RejectsWebhookPathWithoutToken on the shared Auth()
// middleware (which Reuses extractAuthToken + compareTokens). What
// RequireAdminToken adds on top is the *narrowness* test: a worker
// token MUST NOT suffice. The empty-AdminToken test pins the
// fail-closed behaviour against a misconfig that would otherwise let
// every request through.
//
// PG-006 (June 2026): the previous fakes used `&config.Config{...}`
// literals — those required an `internal/platform/config` import
// that the package no longer carries. Replaced with the testSecurity
// stub from port_fakes_test.go (3-method AuthSecurityPort fake).

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRequireAdminToken_RejectsWorkerToken is the load-bearing
// differentiator from Auth(): a worker token MUST NOT authenticate an
// admin-only endpoint. If this test stops passing, either Auth() and
// RequireAdminToken have accidentally converged, or the worker-token
// bypass has been re-introduced.
func TestRequireAdminToken_RejectsWorkerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := &testSecurity{enabled: true, admin: "admin-secret", worker: "worker-secret"}
	r := gin.New()
	r.Use(RequireAdminToken(sec, nil))
	r.GET("/admin-only", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Worker token via the same header channel as the admin token:
	// must NOT suffice.
	req := httptest.NewRequest("GET", "/admin-only", nil)
	req.Header.Set("X-Velox-Admin-Token", "worker-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for worker token on admin endpoint, got %d", w.Code)
	}

	// Worker token via Authorization Bearer: must NOT suffice.
	req = httptest.NewRequest("GET", "/admin-only", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for worker token (Bearer) on admin endpoint, got %d", w.Code)
	}

	// Sanity: the admin token still authenticates (so the test isn't
	// accidentally failing because the middleware refuses everything).
	req = httptest.NewRequest("GET", "/admin-only", nil)
	req.Header.Set("X-Velox-Admin-Token", "admin-secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin token, got %d", w.Code)
	}
}

// TestRequireAdminToken_EmptyAdminTokenRefusesRequest pins the
// fail-closed behaviour: a misconfig where EnableAuth=true but
// AdminToken is empty must NOT silently permit every request. The
// expected response is 500 (server misconfiguration) rather than
// 401 (which would imply the requester is suspicious) — the operator
// is at fault, not the caller.
func TestRequireAdminToken_EmptyAdminTokenRefusesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sec := &testSecurity{enabled: true, admin: "", worker: "worker-secret"}
	r := gin.New()
	r.Use(RequireAdminToken(sec, nil))
	r.GET("/admin-only", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Even a request that would be authenticated in a healthy
	// config must be rejected — fail closed.
	req := httptest.NewRequest("GET", "/admin-only", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty AdminToken (fail-closed), got %d", w.Code)
	}
}

// TestRequireAdminToken_DisabledPassesThrough pins the opt-out
// behaviour. When EnableAuth=false the middleware bypasses every
// request. sec=nil also bypasses. Tests confirm both — the dual
// bypass is intentional (test fixtures and partial compose-root
// builds may legitimately pass nil port).
func TestRequireAdminToken_DisabledPassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("EnableAuth=false", func(t *testing.T) {
		sec := &testSecurity{enabled: false, admin: "ignored"}
		r := gin.New()
		r.Use(RequireAdminToken(sec, nil))
		r.GET("/admin-only", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest("GET", "/admin-only", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 when auth disabled, got %d", w.Code)
		}
	})

	t.Run("nil sec", func(t *testing.T) {
		r := gin.New()
		r.Use(RequireAdminToken(nil, nil))
		r.GET("/admin-only", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})
		req := httptest.NewRequest("GET", "/admin-only", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 when sec is nil (test/partial-build path), got %d", w.Code)
		}
	})
}
