package middleware

import (
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RequireAdminToken returns a gin middleware that enforces the
// X-Velox-Admin-Token check on each request.
//
// Token sources (in priority order):
//
//	1. X-Velox-Admin-Token header (preferred — explicit, doesn't share
//	   the Authorization channel with worker tokens or future OAuth flows)
//	2. Authorization: Bearer <token> header (legacy fallback that
//	   mirrors the previous requireJobAuth behaviour so existing
//	   internal scripts keep working)
//
// Comparison goes through compareTokens (crypto/subtle) so the secret
// is not leaked via byte-by-byte network-level timing — see that
// function's docstring for the threat model.
//
// Distinct from Auth(): Auth() accepts BOTH admin and worker tokens,
// because the /api/* protected group covers a mix of operator-facing
// and worker-broker-facing endpoints. RequireAdminToken is admin-only
// and is the right gate for endpoints where a worker credential MUST
// NOT suffice:
//
//	- /api/script/jobs/:job_id          job status lookup
//	- /api/script/jobs/:job_id/full     full job state
//	- any future admin-restricted endpoint added under /api/script/
//
// Distinct from WorkerAuth(): WorkerAuth refuses admin tokens and
// accepts only the worker token. RequireAdminToken refuses worker
// tokens and accepts only the admin token. The two are mirror
// images; together with Auth() they form the auth-triplet of
// middleware in this package.
//
// Failure modes:
//   - cfg == nil OR cfg.Security.EnableAuth == false → pass-through
//     (the middleware is opt-out via configuration; matches the
//     requireJobAuth pre-extraction semantics).
//   - cfg.Security.AdminToken empty AND EnableAuth == true → 500
//     (no secret to compare against — fail loud rather than silently
//     permitting every request).
//   - Provided token does not byte-match the configured AdminToken
//     (constant-time compare) → 401 with structured JSON, Abort so
//     downstream handlers don't run.
//
// SECURITY: never logs the token value. Only the boolean
// `has_credential` is logged — Auth() protects the same invariant
// for the same reason (the token leaks into the journal and the
// persistent api_requests table otherwise, and even the hash/prefix
// of a short admin token is a side-channel against timing attacks).
func RequireAdminToken(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.Security.EnableAuth {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		expected := strings.TrimSpace(cfg.Security.AdminToken)
		if expected == "" {
			// Refuse to mount effectively-unauthenticated admin
			// endpoints. A blank AdminToken in production would
			// silently let every caller through; fail closed at first
			// request rather than wait for a security incident to
			// surface the misconfig.
			logger.Error("RequireAdminToken mounted with empty AdminToken — refusing request",
				zap.String("path", c.Request.URL.Path),
				zap.String("client_ip", c.ClientIP()))
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "RequireAdminToken misconfigured (VELOX_ADMIN_TOKEN is empty)",
			})
			c.Abort()
			return
		}

		provided := extractAuthToken(c)

		// SECURITY: never log token value — see package-level test
		// TestAuth_NeverPersistsTokenValue for the audit story.
		logger.Info("RequireAdminToken check",
			zap.String("path", c.Request.URL.Path),
			zap.Bool("has_credential", provided != ""))

		if compareTokens(provided, expected) {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		logger.Warn("RequireAdminToken rejected request",
			zap.String("path", c.Request.URL.Path),
			zap.Bool("has_credential", provided != ""),
			zap.String("client_ip", c.ClientIP()))
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "admin token required",
		})
		c.Abort()
	}
}
