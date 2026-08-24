package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Auth returns a gin middleware for authentication.
//
// PG-006 (June 2026): the previous signature took *config.Config
// (`internal/platform/config`) and used the package-level
// logger aliases from `internal/infrastructure/logging`. The middleware
// is now strictly domain-shaped — AuthSecurityPort (defined in
// internal/application/middleware/ports.go) carries the bool + token
// values, and *zap.Logger is passed at registration time.
func Auth(sec middleware.AuthSecurityPort, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sec != nil && !sec.EnableAuth() {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		token := extractAuthToken(c)

		if log != nil {
			log.Info("Auth check",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", token != ""))
		}

		if sec != nil && CompareTokens(token, sec.AdminToken()) {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		if sec != nil && CompareTokens(token, sec.WorkerToken()) {
			c.Set("is_worker", true)
			c.Next()
			return
		}

		if log != nil {
			log.Warn("Unauthorized access attempt",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", token != ""),
				zap.String("client_ip", c.ClientIP()))
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "Unauthorized",
		})
		c.Abort()
	}
}

func extractAuthToken(c *gin.Context) string {
	if token := strings.TrimSpace(c.GetHeader("X-Velox-Admin-Token")); token != "" {
		return token
	}

	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader != "" {
		const bearerPrefix = "Bearer "
		if strings.HasPrefix(authHeader, bearerPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
		}
		return authHeader
	}

	// Session cookie support for the admin SPA. The cookie value is the
	// admin token itself, so it flows through the same constant-time
	// comparison as header-based tokens.
	if cookie, err := c.Cookie("velox_admin_session"); err == nil && strings.TrimSpace(cookie) != "" {
		return strings.TrimSpace(cookie)
	}

	return ""
}

// CompareTokens returns true iff `provided` and `expected` are
// non-empty, equal length, and byte-equal — computed in constant time
// via crypto/subtle.ConstantTimeCompare.
func CompareTokens(provided, expected string) bool {
	// PR 14 (June 2026): trim whitespace on both sides.
	// Systemd's Environment= directive may carry trailing spaces
	// or invisible characters that become part of the token value.
	// The caller (Auth) already trims the provided token via
	// extractAuthToken, but the expected token from cfg.Security
	// was NOT trimmed — a length mismatch in the byte-exact
	// comparison below silently rejected every request.
	// TrimSpace here mirrors the RequireAdminToken trimming
	// on the expected-token side, and is a defence-in-depth
	// against any future whitespace-injection vector.
	provided = strings.TrimSpace(provided)
	expected = strings.TrimSpace(expected)
	if provided == "" || expected == "" {
		return false
	}
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// WorkerAuth returns a gin middleware that accepts ONLY worker
// tokens. Defense-in-depth mirror of Auth() — see the long comment on
// the previous implementation in the git history (PR1 era, June
// 2026) for threat model.
//
// PG-006 (June 2026): zero infra imports; takes AuthSecurityPort +
// *zap.Logger.
func WorkerAuth(sec middleware.AuthSecurityPort, log *zap.Logger) gin.HandlerFunc {
	if sec == nil {
		// Pre-token resolution: refuse to mount unauthenticated routes.
		return func(c *gin.Context) {
			if log != nil {
				log.Error("WorkerAuth mounted without AuthSecurityPort — refusing request",
					zap.String("path", c.Request.URL.Path),
					zap.String("client_ip", c.ClientIP()))
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "WorkerAuth misconfigured (no AuthSecurityPort supplied)",
			})
			c.Abort()
		}
	}

	expected := strings.TrimSpace(sec.WorkerToken())
	if expected == "" {
		return func(c *gin.Context) {
			if log != nil {
				log.Error("WorkerAuth mounted with empty WorkerToken — refusing request",
					zap.String("path", c.Request.URL.Path),
					zap.String("client_ip", c.ClientIP()))
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"ok":    false,
				"error": "WorkerAuth misconfigured (VELOX_WORKER_TOKEN is empty)",
			})
			c.Abort()
		}
	}

	return func(c *gin.Context) {
		// PG-006 consistency: mirror Auth()'s EnableAuth bypass.
		// When auth is disabled (dev/test/E2E), every principal is
		// treated as admin so WorkspaceScopeMiddleware respects the
		// X-Workspace-ID header and the search handler receives a
		// non-default workspace. In production (EnableAuth=true)
		// WorkerAuth remains strict worker-token-only.
		if !sec.EnableAuth() {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		token := extractAuthToken(c)

		if log != nil {
			log.Info("WorkerAuth check",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", token != ""))
		}

		// PR-B defense-in-depth: ONLY worker tokens are accepted on
		// /internal/v1/*. Admin tokens (whether delivered via
		// X-Velox-Admin-Token or Authorization: Bearer) MUST be
		// rejected — otherwise a leaked admin token can claim
		// worker jobs remotely, and the server/worker separation
		// becomes cosmetic. This is a load-bearing security
		// invariant pinned by TestWorkerAuth_RejectsAdminToken;
		// touching it requires re-validating the threat model.
		if CompareTokens(token, expected) {
			c.Set("is_worker", true)
			c.Next()
			return
		}

		if log != nil {
			log.Warn("WorkerAuth rejected request (token did not match configured worker token)",
				zap.String("path", c.Request.URL.Path),
				zap.Bool("has_credential", token != ""),
				zap.String("client_ip", c.ClientIP()))
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "Unauthorized: worker token required",
		})
		c.Abort()
	}
}

// Recovery returns a gin middleware for recovering from panics.
//
// PG-006 (June 2026): previously called the package-level
// logger.Error alias; now takes *zap.Logger at registration time.
func Recovery(log *zap.Logger) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, err any) {
		if log != nil {
			log.Error("Panic recovered",
				zap.Any("error", err),
				zap.String("path", c.Request.URL.Path),
			)
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Internal server error",
		})
	})
}

// RequestID returns a gin middleware that adds a unique ID to each
// request and stores it both in the gin context and the request's
// context.Context (as a correlation id, retrievable via
// corid.FromContext).
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		} else {
			reqID = sanitizeRequestID(reqID)
		}
		c.Set("request_id", reqID)
		c.Header("X-Request-ID", reqID)
		c.Request = c.Request.WithContext(corid.WithCorrelationID(c.Request.Context(), reqID))
		c.Next()
	}
}

// generateRequestID creates a cryptographically random request ID.
func generateRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err == nil {
		return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
	}
	return time.Now().Format("20060102-150405") + "-" + randomString(8)
}

// sanitizeRequestID clamps length and strips non-alphanumeric
// characters to prevent log injection from client-supplied
// X-Request-ID headers.
func sanitizeRequestID(raw string) string {
	const maxLen = 64
	if len(raw) > maxLen {
		raw = raw[:maxLen]
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return generateRequestID()
	}
	return b.String()
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}

func GetUserID(c *gin.Context) string {
	if admin, _ := c.Get("is_admin"); admin == true {
		return "admin"
	}
	if worker, _ := c.Get("is_worker"); worker == true {
		return "worker"
	}
	return "anonymous"
}
