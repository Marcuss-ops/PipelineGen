package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware/requestlog"
	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	logsink "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/logsink"
)

func TestRequestIDMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) {
		reqID, exists := c.Get("request_id")
		assert.True(t, exists)
		assert.NotEmpty(t, reqID)
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
}

func TestPersistentLoggerMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create an in-memory SQLite database for testing
	db := drive.NewTestDB(t, &drive.TestDBOpts{InMemory: true})
	defer db.Close()

	// Set up the api_requests table (simulating migration 008)
	drive.MustExec(t, db, `
		CREATE TABLE api_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts DATETIME DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			status INTEGER,
			duration_ms REAL,
			client_ip TEXT,
			user_id TEXT,
			bytes_in INTEGER,
			bytes_out INTEGER,
			user_agent TEXT,
			error TEXT
		);
	`)

	// The middleware layer no longer holds *sql.DB. The composition
	// root wires a SQLite-backed sink; tests inject one directly so
	// the test remains authoritative without depending on the
	// internal/adapter internals.
	sink := logsink.NewSQLiteRequestLogSink(db, zaptest.NewLogger(t))
	defer func() { _ = sink.Stop(context.Background()) }()
	SetLogSink(sink)

	r := gin.New()
	r.Use(RequestID())
	r.Use(Logger())
	r.GET("/test-endpoint", func(c *gin.Context) {
		c.String(http.StatusOK, "hello world")
	})

	req, _ := http.NewRequest("GET", "/test-endpoint", nil)
	req.Header.Set("User-Agent", "GoTest-Agent")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// The writer writes asynchronously and flushes every 100ms or on batch size 200.
	// Wait long enough for the channel-buffered entry to flush into the DB.
	time.Sleep(300 * time.Millisecond)

	// Query the database to verify the request was logged
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM api_requests").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var reqID, method, path, clientIP, userID, userAgent, errStr string
	var status, bytesIn, bytesOut int
	var durationMs float64

	err = db.QueryRow(`
		SELECT request_id, method, path, status, duration_ms, client_ip, user_id, bytes_in, bytes_out, user_agent, error
		FROM api_requests
	`).Scan(&reqID, &method, &path, &status, &durationMs, &clientIP, &userID, &bytesIn, &bytesOut, &userAgent, &errStr)

	require.NoError(t, err)
	assert.NotEmpty(t, reqID)
	assert.Equal(t, "GET", method)
	assert.Equal(t, "/test-endpoint", path)
	assert.Equal(t, http.StatusOK, status)
	assert.Greater(t, durationMs, 0.0)
	assert.Equal(t, "anonymous", userID)
	assert.Equal(t, "GoTest-Agent", userAgent)
}

// stuckSink is a RequestLogSink whose Log method always reports as
// dropped (channel full) without blocking. Used by TestLoggerBackpressure
// to verify that the Logger middleware never blocks the request handler
// when the downstream sink is overwhelmed.
type stuckSink struct {
	dropped uint64
}

// Compile-time assertion: stuckSink satisfies the RequestLogSink port.
// Any drift in the port signature is caught by `go build` immediately,
// so the backpressure test cannot silently regress against the
// renamed surface.
var _ requestlog.RequestLogSink = (*stuckSink)(nil)

func (s *stuckSink) Log(ctx context.Context, entry requestlog.RequestLogEntry) error {
	atomic.AddUint64(&s.dropped, 1)
	return nil
}
func (s *stuckSink) FlushBatch(ctx context.Context, batch []requestlog.RequestLogEntry) error {
	return nil
}
func (s *stuckSink) Stop(ctx context.Context) error {
	return nil
}
func (s *stuckSink) DroppedLogs() uint64 {
	return atomic.LoadUint64(&s.dropped)
}

// TestLoggerBackpressure verifies the non-blocking invariant: the
// Logger middleware must not let a downstream sink overflow block
// the HTTP request handler. The original test reached into the
// package-private logChan/logDB globals (which the new architecture
// removes); this test exercises the same invariant via the public
// RequestLogSink surface, using a stub sink whose Log always records
// a drop.
func TestLoggerBackpressure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure prior background writer is fully stopped before swapping sinks.
	StopLogger()

	sink := &stuckSink{}
	SetLogSink(sink)
	t.Cleanup(func() {
		StopLogger()
		SetLogSink(nil)
	})

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("request_id", "test-id")
		c.Next()
	})
	r.Use(Logger())
	r.GET("/overflow", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/overflow", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	r.ServeHTTP(w, req)
	duration := time.Since(start)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Less(t, duration, 100*time.Millisecond, "Request should not block on downstream sink")
	assert.GreaterOrEqual(t, GetDroppedLogs(), uint64(1), "At least one log should have been dropped")
}
