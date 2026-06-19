package middleware

import (
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/logging"
)

// apiLog represents a logged API request for the database
type apiLog struct {
	RequestID string
	Method    string
	Path      string
	Status    int
	Duration  time.Duration
	IP        string
	UserID    string
	BytesIn   int
	BytesOut  int
	UA        string
	Err       string
}

var (
	logDB       *sql.DB
	logChan     = make(chan apiLog, 5000)
	droppedLogs uint64
	writerWG    sync.WaitGroup
	stopChan    chan struct{}
)

// SetLogDB sets the database for persistent logging
func SetLogDB(db *sql.DB) {
	logDB = db
	if stopChan == nil {
		stopChan = make(chan struct{})
		writerWG.Add(1)
		go apiLogWriter()
	}
}

// StopLogger flushes and stops the background log writer
func StopLogger() {
	if stopChan == nil {
		return
	}
	close(stopChan)
	writerWG.Wait()
	stopChan = nil
}

// GetDroppedLogs returns the number of logs dropped due to backpressure
func GetDroppedLogs() uint64 {
	return atomic.LoadUint64(&droppedLogs)
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

func apiLogWriter() {
	defer writerWG.Done()
	if logDB == nil {
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	batch := make([]apiLog, 0, 200)

	for {
		select {
		case l := <-logChan:
			batch = append(batch, l)
			if len(batch) >= 200 {
				flushLogs(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flushLogs(batch)
				batch = batch[:0]
			}
		case <-stopChan:
			// Drain remaining logs without closing logChan (to allow restart/reuse in tests)
			// In production, the process is exiting anyway.
		drain:
			for {
				select {
				case l := <-logChan:
					batch = append(batch, l)
					if len(batch) >= 200 {
						flushLogs(batch)
						batch = batch[:0]
					}
				default:
					break drain
				}
			}
			if len(batch) > 0 {
				flushLogs(batch)
			}
			return
		}
	}
}

func flushLogs(batch []apiLog) {
	if logDB == nil {
		return
	}

	tx, err := logDB.Begin()
	if err != nil {
		logger.Error("Failed to start log transaction", zap.Error(err))
		return
	}

	stmt, err := tx.Prepare(`
      INSERT INTO api_requests 
      (request_id, method, path, status, duration_ms, client_ip, user_id, bytes_in, bytes_out, user_agent, error)
      VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		logger.Error("Failed to prepare log statement", zap.Error(err))
		tx.Rollback()
		return
	}
	defer stmt.Close()

	for _, l := range batch {
		_, err := stmt.Exec(l.RequestID, l.Method, l.Path, l.Status, float64(l.Duration.Microseconds())/1000.0,
			l.IP, l.UserID, l.BytesIn, l.BytesOut, l.UA, l.Err)
		if err != nil {
			logger.Warn("Failed to execute log insert", zap.Error(err))
		}
	}

	if err := tx.Commit(); err != nil {
		logger.Error("Failed to commit logs", zap.Error(err))
	}
}

// Logger returns a gin middleware for logging requests
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
		isHealth := c.FullPath() == "/health" || c.FullPath() == "/api/health"

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

		// Send to async persistent logger
		if !isHealth && logDB != nil {
			reqID, _ := c.Get("request_id")
			reqIDStr, _ := reqID.(string)

			entry := apiLog{
				RequestID: reqIDStr,
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

			// Non-blocking send with backpressure tracking
			select {
			case logChan <- entry:
			default:
				atomic.AddUint64(&droppedLogs, 1)
			}
		}
	}
}
