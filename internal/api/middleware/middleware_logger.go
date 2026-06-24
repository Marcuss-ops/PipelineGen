package middleware

import (
	"context"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware/requestlog"
	logger "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetLogSink installs the persistent logger sink. The middleware no
// longer holds *sql.DB — the SQLite-backed implementation lives in
// internal/infrastructure/database/sqlite/logsink and is injected
// from the composition root. A nil sink is treated as a no-op.
func SetLogSink(sink requestlog.RequestLogSink) {
	logSink = sink
}

// StopLogger flushes and stops the background log writer. Delegated
// entirely to the sink; idempotent because SQLiteRequestLogSink.Stop
// closes its stopChan and drains via sync.Once.
func StopLogger() {
	if logSink == nil {
		return
	}
	_ = logSink.Stop(context.Background())
}

// logSink is the typed persistent sink. Nil is treated as a no-op so
// environments that don't need request logging (tests, dry-run CLI)
// can leave it unset.
var logSink requestlog.RequestLogSink

// GetDroppedLogs returns the number of logs dropped due to backpressure.
// Reflects the SQLite-backed sink's counter when the sink exposes it.
func GetDroppedLogs() uint64 {
	if sink, ok := logSink.(interface{ DroppedLogs() uint64 }); ok {
		return sink.DroppedLogs()
	}
	return 0
}

// sensitiveQueryKeys lists URL query parameter names whose values are
// redacted from logs to prevent credential leakage.
var sensitiveQueryKeys = map[string]struct{}{
	"token":        {},
	"api_key":      {},
	"apikey":       {},
	"key":          {},
	"secret":       {},
	"password":     {},
	"auth":         {},
	"credential":   {},
	"access_token": {},
}

// redactSensitiveQuery replaces the value of any known-sensitive query
// parameter with `[REDACTED]` while preserving the rest of the query
// string. An empty input is returned as-is.
func redactSensitiveQuery(raw string) string {
	if raw == "" {
		return raw
	}
	// Fast path: no '=' at all, no sensitive param to redact.
	if !strings.ContainsAny(raw, "=&") {
		return raw
	}
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		name := strings.ToLower(p[:eq])
		if _, ok := sensitiveQueryKeys[name]; ok {
			parts[i] = name + "=[REDACTED]"
		}
	}
	return strings.Join(parts, "&")
}

// Logger returns a gin middleware for logging requests.
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		// SECURITY: redact sensitive query parameters (token, key, secret,
		// password) before logging, even though extractAuthToken no longer
		// reads ?token=... — future query parameters or third-party
		// middleware may still surface a credential via the query string.
		raw := redactSensitiveQuery(c.Request.URL.RawQuery)

		c.Next()

		// Skip health check logging to database if desired, but keep in journal
		isHealth := c.FullPath() == "/health"

		duration := time.Since(start)
		status := c.Writer.Status()

		// Efficient fields allocation
		fields := make([]zap.Field, 0, 8)
		fields = append(fields,
			zap.Int("status", status),
			zap.Duration("duration", duration),
			zap.String("path", path),
			zap.String("method", c.Request.Method),
			zap.String("client_ip", c.ClientIP()),
		)

		if raw != "" {
			fields = append(fields, zap.String("query", raw))
		}

		if len(c.Errors) > 0 {
			errs := make([]error, len(c.Errors))
			for i, e := range c.Errors {
				errs[i] = e
			}
			fields = append(fields, zap.Errors("errors", errs))
		}

		// Log to journal based on status code
		switch {
		case status >= 500:
			logger.Error("Server error", fields...)
		case status >= 400:
			logger.Warn("Client error", fields...)
		default:
			logger.Info("Request completed", fields...)
		}

		// Persist via the typed sink (no raw *sql.DB in this layer).
		if !isHealth && logSink != nil {
			reqID, _ := c.Get("request_id")
			reqIDStr, _ := reqID.(string)

			entry := loggerEntryFrom(c, reqIDStr, duration, status)
			_ = logSink.Log(c.Request.Context(), entry)
		}
	}
}

// loggerEntryFrom assembles a RequestLogEntry from the gin context.
// Extracted to keep Logger() readable and to give tests a stable
// entry-builder they can compare against table contents.
func loggerEntryFrom(c *gin.Context, requestID string, duration time.Duration, status int) requestlog.RequestLogEntry {
	return requestlog.RequestLogEntry{
		RequestID: requestID,
		Method:    c.Request.Method,
		Path:      c.FullPath(),
		Status:    status,
		Duration:  duration,
		IP:        c.ClientIP(),
		UserID:    GetUserID(c),
		BytesIn:   int(c.Request.ContentLength),
		BytesOut:  c.Writer.Size(),
		UA:        c.Request.UserAgent(),
		Err:       c.Errors.String(),
	}
}
