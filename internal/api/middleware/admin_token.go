package middleware

import (
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RequireAdminToken returns a gin middleware that enforces the
// X-Velox-Admin-Token check on each request.
//
// PG-006 (June 2026): the previous signature took *config.Config
// (`internal/platform/config`) and called the package-level
// logger.Error/Info/Warn from `internal/infrastructure/logging`. The
// middleware is now strictly transport — security settings flow
// through middleware.AuthSecurityPort (defined in
// internal/application/middleware/ports.go) and the structured logger
// is injected as a *zap.Logger arg. The adapter at
// internal/app/middleware_security_adapter.go wraps *config.Config at
// composition time.
//
// Token sources (in priority order):
//  1. X-Velox-Admin-Token header (preferred — explicit, doesn't share
//     the Authorization channel with worker tokens or future OAuth flows)
//  2. Authorization: Bearer <token> header (legacy fallback)
//
// Comparison goes through CompareTokens (crypto/subtle) so the secret
// is not leaked via byte-by-byte network-level timing.
//
// Distinct from Auth(): Auth() accepts BOTH admin and worker tokens;
// RequireAdminToken is admin-only.
//
// Failure modes:
//   - sec == nil OR sec.EnableAuth() == false → pass-through (set
//     "is_admin" so downstream handlers still treat the request as
//     admin-context — pre-extraction semantics preserved).
//   - sec.AdminToken() empty AND EnableAuth() == true → 500 (no secret
//     to compare against — fail loud rather than silently permitting
//     every request).
//   - Provided token does not byte-match the configured AdminToken
//     (constant-time compare) → 401 with structured JSON, Abort.
//
// SECURITY: never logs the token value; only `has_credential` bool.
func RequireAdminToken(sec middleware.AuthSecurityPort, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sec == nil || !sec.EnableAuth() {
			c.Set("is_admin", true)
			c.Next()
			return
		}
		expected := strings.TrimSpace(sec.AdminToken())
		if expected == "" {
			if log != nil {
				log.Error("RequireAdminToken mounted with empty AdminToken — refusing request",
					zap.String("path", c.Request.URL.Path),
					zap.String("client_ip", c.ClientIP()))
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "RequireAdminToken misconfigured (VELOX_ADMIN_TOKEN is empty)",
			})
			c.Abort()
			return
		}

		provided := extractAuthToken(c)

		if log != nil {
			log.Info("RequireAdminToken check",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", provided != ""))
		}

		if CompareTokens(provided, expected) {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		if log != nil {
			log.Warn("RequireAdminToken rejected request",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", provided != ""),
				zap.String("client_ip", c.ClientIP()))
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "admin token required",
		})
		c.Abort()
	}
}
