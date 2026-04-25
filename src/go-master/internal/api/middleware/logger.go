package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// RequestLogEntry represents a logged API request
type RequestLogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	ErrorType  string    `json:"error_type,omitempty"`
	ClientIP   string    `json:"client_ip"`
	Method     string    `json:"method"`
}

var (
	requestLogs []RequestLogEntry
	logsMu      sync.RWMutex
	maxLogs     = 1000
)

// Logger returns a gin middleware for logging requests
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		fields := []zap.Field{
			zap.Int("status", status),
			zap.Duration("duration", duration),
			zap.String("path", path),
			zap.String("method", c.Request.Method),
			zap.String("client_ip", c.ClientIP()),
		}

		if raw != "" {
			fields = append(fields, zap.String("query", raw))
		}

		if len(c.Errors) > 0 {
			// Convert string errors to error type
			errs := make([]error, len(c.Errors))
			for i, e := range c.Errors {
				errs[i] = e
			}
			fields = append(fields, zap.Errors("errors", errs))
		}

		// Log based on status code
		switch {
		case status >= 500:
			logger.Error("Server error", fields...)
		case status >= 400:
			logger.Warn("Client error", fields...)
		default:
			logger.Debug("Request completed", fields...)
		}

		// Add to request logs
		entry := RequestLogEntry{
			Timestamp:  time.Now(),
			Path:       path,
			Status:     status,
			DurationMS: duration.Milliseconds(),
			ClientIP:   c.ClientIP(),
			Method:     c.Request.Method,
		}

		if status >= 400 {
			entry.ErrorType = http.StatusText(status)
		}

		addRequestLog(entry)
	}
}

// GetRequestLogs returns the recent request logs
func GetRequestLogs() []RequestLogEntry {
	logsMu.RLock()
	defer logsMu.RUnlock()

	logs := make([]RequestLogEntry, len(requestLogs))
	copy(logs, requestLogs)
	return logs
}

func addRequestLog(entry RequestLogEntry) {
	logsMu.Lock()
	defer logsMu.Unlock()

	requestLogs = append(requestLogs, entry)
	if len(requestLogs) > maxLogs {
		requestLogs = requestLogs[len(requestLogs)-maxLogs:]
	}
}