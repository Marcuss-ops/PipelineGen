package middleware

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware/requestlog"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetLogSink installs the persistent logger sink. The middleware no
// longer holds *sql.DB — the SQLite-backed implementation lives in
// internal/platform/sqlite/logsink and is injected
// from the composition root. A nil sink is treated as a no-op.
func SetLogSink(sink requestlog.RequestLogSink) {
	if sink == nil {
		logSink.Store(nil)
		return
	}
	logSink.Store(&logSinkHolder{sink: sink})
}

// StopLogger flushes and stops the background log writer. Delegated
// entirely to the sink; idempotent.
func StopLogger() {
	sink := currentLogSink()
	if sink == nil {
		return
	}
	_ = sink.Stop(context.Background())
}

// logSinkHolder keeps the interface in an atomic.Pointer so the wiring can
// install the sink while request goroutines are already reading it. A plain
// package variable was a data race whenever the sink was set after the server
// started (and in every test that swapped it).
type logSinkHolder struct{ sink requestlog.RequestLogSink }

var logSink atomic.Pointer[logSinkHolder]

// currentLogSink returns the installed sink, or nil when none is configured.
func currentLogSink() requestlog.RequestLogSink {
	if holder := logSink.Load(); holder != nil {
		return holder.sink
	}
	return nil
}

// GetDroppedLogs returns the number of logs dropped due to backpressure.
func GetDroppedLogs() uint64 {
	if sink, ok := currentLogSink().(interface{ DroppedLogs() uint64 }); ok {
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
//
// PG-006 (June 2026): the previous body called package-level
// `logger.Error/Info/Warn` from internal/platform/logging. The
// middleware now takes a *zap.Logger directly — the AdapterLayer
// (composition root) hands the standard zap logger at registration
// time. This file has zero `internal/platform/*` imports.
func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := redactSensitiveQuery(c.Request.URL.RawQuery)

		c.Next()

		isHealth := c.FullPath() == "/health"

		duration := time.Since(start)
		status := c.Writer.Status()

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

		if log != nil {
			switch {
			case status >= 500:
				log.Error("Server error", fields...)
			case status >= 400:
				log.Warn("Client error", fields...)
			default:
				log.Info("Request completed", fields...)
			}
		}

		// Persist via the typed sink (no raw *sql.DB in this layer).
		if sink := currentLogSink(); !isHealth && sink != nil {
			reqID, _ := c.Get("request_id")
			reqIDStr, _ := reqID.(string)

			entry := loggerEntryFrom(c, reqIDStr, duration, status)
			_ = sink.Log(c.Request.Context(), entry)
		}
	}
}

// loggerEntryFrom assembles a RequestLogEntry from the gin context.
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
