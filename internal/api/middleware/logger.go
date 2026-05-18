package middleware

import (
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/logger"
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
	logDB   *sql.DB
	logChan = make(chan apiLog, 5000)
)

// SetLogDB sets the database for persistent logging
func SetLogDB(db *sql.DB) {
	logDB = db
	go apiLogWriter()
}

func apiLogWriter() {
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
		raw := c.Request.URL.RawQuery

		c.Next()

		// Skip health check logging to database if desired, but keep in journal
		isHealth := c.FullPath() == "/health" || c.FullPath() == "/api/health"

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
			fields = append(fields, zap.Errors("errors", c.Errors.Errors()))
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

			logChan <- apiLog{
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
		}
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
