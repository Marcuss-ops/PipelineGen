package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
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

	// Set the database for the logger
	SetLogDB(db)

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

	// The writer writes asynchronously and flushes every 100ms or on batch size 200
	// We wait 250ms to ensure it has flushed
	time.Sleep(250 * time.Millisecond)

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

func TestLoggerBackpressure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure background writer is stopped
	StopLogger()

	// Reset dropped counter
	atomic.StoreUint64(&droppedLogs, 0)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("request_id", "test-id")
		c.Next()
	})
	r.Use(Logger())
	r.GET("/overflow", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Set a mock logDB directly WITHOUT calling SetLogDB (to not start writer)
	dummyDB := drive.NewTestDB(t, &drive.TestDBOpts{InMemory: true})
	defer dummyDB.Close()
	logDB = dummyDB

	// Fill the channel completely until it blocks
	for {
		select {
		case logChan <- apiLog{}:
		default:
			goto full
		}
	}
full:

	// Next request should be dropped
	req, _ := http.NewRequest("GET", "/overflow", nil)
	w := httptest.NewRecorder()

	start := time.Now()
	r.ServeHTTP(w, req)
	duration := time.Since(start)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Less(t, duration, 100*time.Millisecond, "Request should not block even when log queue is full")
	assert.GreaterOrEqual(t, GetDroppedLogs(), uint64(1), "At least one log should have been dropped")

	// Cleanup: drain the channel so other tests aren't affected
drain:
	for {
		select {
		case <-logChan:
		default:
			break drain
		}
	}
	logDB = nil
}
