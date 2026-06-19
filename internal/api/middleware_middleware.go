package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/logging"
	corid "github.com/Marcuss-ops/PipelineGen/internal/platform"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Auth returns a gin middleware for authentication
func Auth(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Security.EnableAuth {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		token := extractAuthToken(c)
		// SECURITY: never log the token itself (not even hashed / truncated).
		// Logging `received_token` / `expected_token` / `token` leaks the
		// full secret into the journal and the persistent api_requests
		// table via the request logger. We intentionally log only a
		// boolean — not the credential's length, prefix, or hash — to
		// also avoid timing-style attacks against short admin tokens.
		logger.Info("Auth check",
			zap.String("path", c.Request.URL.Path),
			zap.Bool("has_credential", token != ""))

		// Check admin token. Comparison MUST go through compareTokens
		// (crypto/subtle.ConstantTimeCompare) to resist byte-by-byte
		// network-level timing attacks — Go's `==` short-circuits on
		// the first byte mismatch, leaking the secret's prefix.
		if compareTokens(token, cfg.Security.AdminToken) {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		// Check worker token (constant-time — see compareTokens doc).
		if compareTokens(token, cfg.Security.WorkerToken) {
			c.Set("is_worker", true)
			c.Next()
			return
		}

		logger.Warn("Unauthorized access attempt",
			zap.String("path", c.Request.URL.Path),
			zap.Bool("has_credential", token != ""),
			zap.String("client_ip", c.ClientIP()))
		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "Unauthorized",
		})
		c.Abort()
	}
}

func extractAuthToken(c *gin.Context) string {
	// SECURITY: tokens are only accepted via HTTP headers, never via
	// query string. Query strings are routinely logged by reverse
	// proxies, browser history, and our own request logger; allowing
	// ?token=... would leak the secret into multiple persistent stores.
	//
	// The Authorization header value itself is never logged. Do not add
	// `logger.Debug("got auth", zap.String("header", authHeader))` here.
	if token := strings.TrimSpace(c.GetHeader("X-Velox-Admin-Token")); token != "" {
		return token
	}

	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if strings.HasPrefix(authHeader, bearerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
	}

	return authHeader
}

// compareTokens returns true iff `provided` and `expected` are
// non-empty, equal length, and byte-equal — computed in constant time
// via crypto/subtle.ConstantTimeCompare.
//
// Why constant-time matters here: a network-adjacent attacker who can
// repeatedly send authenticated requests can in principle measure
// microsecond-level response-time differences and recover the token
// byte-by-byte. Go's `==` operator on strings short-circuits on the
// first byte mismatch, leaking that prefix timing. ConstantTimeCompare
// always compares every byte, eliminating this signal.
//
// Length-mismatch returns false immediately (subtle.ConstantTimeCompare
// itself returns 0 on length mismatch, but we short-circuit first to
// keep the helper easy to reason about). The expected token has a
// fixed length set at config time, so the length is not a secret —
// only its bytes are. The empty-provided short-circuit mirrors the
// previous `token != ""` guard the call sites used to spell out by hand.
func compareTokens(provided, expected string) bool {
	if provided == "" || expected == "" {
		return false
	}
	if len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// Recovery returns a gin middleware for recovering from panics
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, err any) {
		logger.Error("Panic recovered",
			zap.Any("error", err),
			zap.String("path", c.Request.URL.Path),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Internal server error",
		})
	})
}

// RequestID returns a gin middleware that adds a unique ID to each request
// and stores it both in the gin context (as "request_id") and in the
// request's context.Context (as a correlation id, retrievable via
// corid.FromContext). Downstream code — background jobs, the Ollama
// client, Python scripts exec'd via subprocess — can pull the same
// value out without having to thread it through every function signature.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = generateRequestID()
		} else {
			// Validate client-provided IDs to prevent log injection and
			// excessively long strings.
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
	// Fallback only if the system's CSPRNG is unavailable.
	return time.Now().Format("20060102-150405") + "-" + randomString(8)
}

// sanitizeRequestID clamps length and strips non-alphanumeric characters
// to prevent log injection from client-supplied X-Request-ID headers.
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
