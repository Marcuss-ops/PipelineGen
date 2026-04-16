package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- RateLimiter Tests ---

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !rl.Allow("192.168.1.1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	if rl.Allow("192.168.1.1") {
		t.Error("4th request should be denied")
	}
}

func TestRateLimiter_DifferentKeys(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	if !rl.Allow("key1") {
		t.Error("first request for key1 should be allowed")
	}
	if rl.Allow("key1") {
		t.Error("second request for key1 should be denied")
	}
	if !rl.Allow("key2") {
		t.Error("first request for key2 should be allowed")
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewRateLimiter(1, 50*time.Millisecond)

	if !rl.Allow("test-key") {
		t.Error("first request should be allowed")
	}
	if rl.Allow("test-key") {
		t.Error("second request within window should be denied")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow("test-key") {
		t.Error("request after window expiry should be allowed")
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rl := NewRateLimiter(10, 50*time.Millisecond)

	for i := 0; i < 5; i++ {
		rl.Allow("key")
	}

	if len(rl.requests) != 1 {
		t.Errorf("expected 1 key in map, got %d", len(rl.requests))
	}

	time.Sleep(100 * time.Millisecond)
	rl.Cleanup()

	if len(rl.requests) != 0 {
		t.Errorf("expected 0 keys after cleanup, got %d", len(rl.requests))
	}
}

func TestRateLimiter_Cleanup_KeepsValid(t *testing.T) {
	rl := NewRateLimiter(100, time.Minute)

	for i := 0; i < 3; i++ {
		key := "recent-key-" + string(rune('0'+i))
		rl.requests[key] = []time.Time{time.Now()}
	}

	rl.Cleanup()

	if len(rl.requests) != 3 {
		t.Errorf("expected 3 keys after cleanup, got %d", len(rl.requests))
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(1000, time.Minute)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			rl.Allow("concurrent-key")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestRateLimiter_RequestTracking(t *testing.T) {
	rl := NewRateLimiter(5, time.Minute)
	key := "tracking-test"

	for i := 0; i < 3; i++ {
		rl.Allow(key)
	}

	times, exists := rl.requests[key]
	if !exists {
		t.Fatal("expected key to exist in requests map")
	}
	if len(times) != 3 {
		t.Errorf("expected 3 timestamps, got %d", len(times))
	}
}

func TestRateLimiter_OldRequestCleanup(t *testing.T) {
	rl := NewRateLimiter(10, 50*time.Millisecond)
	key := "cleanup-test"

	for i := 0; i < 10; i++ {
		rl.Allow(key)
	}

	if rl.Allow(key) {
		t.Error("should be denied after limit reached")
	}

	time.Sleep(100 * time.Millisecond)

	if !rl.Allow(key) {
		t.Error("should be allowed after window expiry")
	}
}

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(50, 2*time.Minute)

	if rl.limit != 50 {
		t.Errorf("limit = %d, want 50", rl.limit)
	}
	if rl.window != 2*time.Minute {
		t.Errorf("window = %v, want 2m", rl.window)
	}
	if rl.requests == nil {
		t.Error("requests map should not be nil")
	}
	if rl.maxKeys != 10000 {
		t.Errorf("maxKeys = %d, want 10000", rl.maxKeys)
	}
}

func TestRateLimiterStruct(t *testing.T) {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    10,
		window:   time.Minute,
		maxKeys:  100,
	}

	if rl.limit != 10 {
		t.Errorf("limit = %d, want 10", rl.limit)
	}
	if rl.window != time.Minute {
		t.Errorf("window = %v, want 1m", rl.window)
	}
	if rl.maxKeys != 100 {
		t.Errorf("maxKeys = %d, want 100", rl.maxKeys)
	}
}

// --- Request Log Tests ---

func TestAddRequestLog(t *testing.T) {
	resetLogs()

	entry := RequestLogEntry{
		Timestamp:  time.Now(),
		Path:       "/api/test",
		Status:     200,
		DurationMS: 50,
		ClientIP:   "127.0.0.1",
		Method:     "GET",
	}

	addRequestLog(entry)

	logs := GetRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Path != "/api/test" {
		t.Errorf("path = %q, want /api/test", logs[0].Path)
	}
	if logs[0].Status != 200 {
		t.Errorf("status = %d, want 200", logs[0].Status)
	}
}

func TestAddRequestLog_MaxLogs(t *testing.T) {
	resetLogs()

	for i := 0; i < maxLogs+10; i++ {
		addRequestLog(RequestLogEntry{
			Timestamp: time.Now(),
			Path:      "/test",
			Status:    200,
		})
	}

	logs := GetRequestLogs()
	if len(logs) > maxLogs {
		t.Errorf("expected at most %d logs, got %d", maxLogs, len(logs))
	}
}

func TestGetRequestLogs_Copy(t *testing.T) {
	resetLogs()

	addRequestLog(RequestLogEntry{Path: "/api/one"})
	addRequestLog(RequestLogEntry{Path: "/api/two"})

	logs1 := GetRequestLogs()
	logs2 := GetRequestLogs()

	if &logs1[0] == &logs2[0] {
		t.Error("GetRequestLogs should return a copy")
	}
}

func TestRequestLogEntry(t *testing.T) {
	entry := RequestLogEntry{
		Timestamp:  time.Now(),
		Path:       "/api/health",
		Status:     200,
		DurationMS: 10,
		ClientIP:   "192.168.1.1",
		Method:     "GET",
	}

	if entry.Path != "/api/health" {
		t.Errorf("Path = %q, want /api/health", entry.Path)
	}
	if entry.Status != 200 {
		t.Errorf("Status = %d, want 200", entry.Status)
	}
	if entry.Method != "GET" {
		t.Errorf("Method = %q, want GET", entry.Method)
	}
}

func TestRequestLogEntry_WithError(t *testing.T) {
	entry := RequestLogEntry{
		Timestamp:  time.Now(),
		Path:       "/api/error",
		Status:     500,
		DurationMS: 100,
		ErrorType:  "Internal Server Error",
		ClientIP:   "10.0.0.1",
		Method:     "POST",
	}

	if entry.ErrorType != "Internal Server Error" {
		t.Errorf("ErrorType = %q, want Internal Server Error", entry.ErrorType)
	}
}

// --- Logger Middleware Tests ---

func TestLoggerMiddleware(t *testing.T) {
	resetLogs()

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	c := &gin.Context{}
	c.Request = httptest.NewRequest("GET", "/api/health", nil)

	engine.Use(Logger())
	engine.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	engine.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	logs := GetRequestLogs()
	if len(logs) == 0 {
		t.Error("expected at least 1 log entry")
	}
}

func TestLoggerMiddleware_404(t *testing.T) {
	resetLogs()

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	c := &gin.Context{}
	c.Request = httptest.NewRequest("GET", "/nonexistent", nil)

	engine.Use(Logger())
	engine.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	engine.ServeHTTP(w, c.Request)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestLoggerMiddleware_500(t *testing.T) {
	resetLogs()

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	c := &gin.Context{}
	c.Request = httptest.NewRequest("GET", "/error", nil)

	engine.Use(Logger())
	engine.GET("/error", func(c *gin.Context) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
	})

	engine.ServeHTTP(w, c.Request)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

// --- Recovery Middleware Tests ---

func TestRecovery(t *testing.T) {
	resetLogs()

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	c := &gin.Context{}
	c.Request = httptest.NewRequest("GET", "/panic", nil)

	engine.Use(Recovery())
	engine.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	engine.ServeHTTP(w, c.Request)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

// --- Helper ---

func resetLogs() {
	logsMu.Lock()
	requestLogs = []RequestLogEntry{}
	logsMu.Unlock()
}

// --- Constant tests ---

func TestMaxLogsConstant(t *testing.T) {
	if maxLogs <= 0 {
		t.Errorf("maxLogs = %d, should be positive", maxLogs)
	}
}
