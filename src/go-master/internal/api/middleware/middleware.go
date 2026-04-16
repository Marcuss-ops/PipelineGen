// Package middleware provides HTTP middleware for the VeloxEditing API.
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// RequestLogEntry represents a logged API request
type RequestLogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Path        string    `json:"path"`
	Status      int       `json:"status"`
	DurationMS  int64     `json:"duration_ms"`
	ErrorType   string    `json:"error_type,omitempty"`
	ClientIP    string    `json:"client_ip"`
	Method      string    `json:"method"`
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

// Auth returns a gin middleware for authentication
func Auth() gin.HandlerFunc {
	cfg := config.Get()
	
	return func(c *gin.Context) {
		if !cfg.Security.EnableAuth {
			c.Next()
			return
		}

		// Check admin token
		token := c.GetHeader("X-Velox-Admin-Token")
		if token != "" && token == cfg.Security.AdminToken {
			c.Set("is_admin", true)
			c.Next()
			return
		}

		// Check worker token
		if token != "" && token == cfg.Security.WorkerToken {
			c.Set("is_worker", true)
			c.Next()
			return
		}

		c.JSON(http.StatusUnauthorized, gin.H{
			"ok":    false,
			"error": "Unauthorized",
		})
		c.Abort()
	}
}

// RateLimiter implements a simple rate limiter
type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.RWMutex
	limit    int
	window   time.Duration
	maxKeys  int         // maximum number of tracked IPs
	stopCh   chan struct{} // signals the cleanup goroutine to stop
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
		maxKeys:  10000, // Prevent unbounded memory growth
		stopCh:   make(chan struct{}),
	}
}

// Allow checks if a request is allowed
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Clean old requests for this key
	var valid []time.Time
	for _, t := range rl.requests[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	// Check limit
	if len(valid) >= rl.limit {
		rl.requests[key] = valid
		return false
	}

	valid = append(valid, now)
	rl.requests[key] = valid
	return true
}

// Cleanup removes expired entries and limits map size.
// It runs periodically until Stop() is called.
func (rl *RateLimiter) Cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.cleanupOnce()
		}
	}
}

// cleanupOnce performs a single cleanup pass
func (rl *RateLimiter) cleanupOnce() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Remove empty entries
	for key, times := range rl.requests {
		var valid []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(rl.requests, key)
		} else {
			rl.requests[key] = valid
		}
	}

	// If map is still too large, remove oldest entries
	if len(rl.requests) > rl.maxKeys {
		// Remove the keys with the oldest timestamps
		type keyTime struct {
			key string
			ts  time.Time
		}
		var oldest []keyTime
		for k, times := range rl.requests {
			if len(times) > 0 {
				oldest = append(oldest, keyTime{k, times[0]})
			}
		}
		// Simple approach: just clear the whole map if too large
		// A more sophisticated approach would use a heap
		if len(oldest) > rl.maxKeys*2 {
			rl.requests = make(map[string][]time.Time)
		}
	}
}

// Stop signals the cleanup goroutine to terminate
func (rl *RateLimiter) Stop() {
	close(rl.stopCh)
}

// RateLimitMiddleware holds the middleware and its associated rate limiter
type RateLimitMiddleware struct {
	Handler gin.HandlerFunc
	limiter *RateLimiter
}

// Stop signals the rate limiter's cleanup goroutine to terminate
func (r *RateLimitMiddleware) Stop() {
	if r.limiter != nil {
		r.limiter.Stop()
	}
}

// RateLimit creates a rate limiting middleware. The returned RateLimitMiddleware
// must have its Stop() method called during server shutdown to prevent goroutine leaks.
func RateLimit() *RateLimitMiddleware {
	cfg := config.Get()
	if !cfg.Security.RateLimitEnabled {
		return &RateLimitMiddleware{
			Handler: func(c *gin.Context) {
				c.Next()
			},
		}
	}

	limiter := NewRateLimiter(cfg.Security.RateLimitRequests, time.Minute)

	// Start periodic cleanup (manages its own ticker and stop channel)
	go limiter.Cleanup()

	return &RateLimitMiddleware{
		Handler: func(c *gin.Context) {
			key := c.ClientIP()
			if !limiter.Allow(key) {
				c.JSON(http.StatusTooManyRequests, gin.H{
					"ok":    false,
					"error": "Rate limit exceeded",
				})
				c.Abort()
				return
			}
			c.Next()
		},
		limiter: limiter,
	}
}

// Recovery returns a gin middleware for recovering from panics
func Recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, err interface{}) {
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