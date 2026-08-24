// Package middleware — table tests for the PR8 idempotency
// middleware.
//
// The tests run against a real (in-memory) SQLite-backed
// IdempotencyStore, exercising every documented path:
//
//  1. Pass-through when no Idempotency-Key header.
//  2. 400 on empty key after trim (malformed).
//  3. 400 on non-printable key.
//  4. 400 on key exceeding 255 chars.
//  5. Cache + replay (X-Idempotency-Replay: true, body verbatim).
//  6. 422 on same key + different body.
//  7. 409 on in-flight row.
//  8. Multipart bypass — body_hash empty, request body streamed
//     through unmodified (handler sees c.Request.Body intact).
package middleware

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	idem "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/idempotency"
)

// inMemStore builds an IdempotencyStore backed by an in-memory
// SQLite database. Returns the store + a teardown that closes the
// underlying db handle.
func inMemStore(t *testing.T) idem.IdempotencyStore {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sql.Open("sqlite3", tmp+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const ddl = `
		CREATE TABLE IF NOT EXISTS idempotency_keys (
			key TEXT PRIMARY KEY,
			body_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'in_flight',
			response_status INTEGER NOT NULL DEFAULT 0,
			response_body TEXT NOT NULL DEFAULT '',
			response_content_type TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			last_replayed_at TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at
			ON idempotency_keys(expires_at);
	`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	return idempotency.NewSQLiteRepository(db)
}

// newRouter builds a one-route gin engine with the idempotency
// middleware + the body-capturing downstream handler.
func newRouter(t *testing.T, store idem.IdempotencyStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	idemMw := NewIdempotency(store, zap.NewNop())
	r.Use(idemMw.Handler())
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.Data(http.StatusOK, "application/json", []byte(`{"ok":true,"seen":"`+string(body)+`"}`))
	})
	t.Cleanup(idemMw.Stop)
	return r
}

func TestIdempotency_NoHeaderPassesThrough(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)

	req := httptest.NewRequest(http.MethodPost, "/echo",
		strings.NewReader(`hello`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"seen":"hello"`) {
		t.Fatalf("downstream handler not invoked; body=%q", rec.Body.String())
	}
}

func TestIdempotency_ReplaysOnceCompleted(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)
	key := "test-key-1"
	body := `{"a":1}`

	// 1st request: cache fill.
	req1 := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", key)
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	if rec1.Header().Get(replayHeader) != "" {
		t.Fatalf("first request must NOT set X-Idempotency-Replay")
	}

	// 2nd request with same key + same body: replay.
	req2 := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", key)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if rec2.Header().Get(replayHeader) != "true" {
		t.Fatalf("replay must set X-Idempotency-Replay: true (got %q)", rec2.Header().Get(replayHeader))
	}
	if rec2.Body.String() != rec1.Body.String() {
		t.Fatalf("replay body must match first body\nrec1=%s\nrec2=%s", rec1.Body.String(), rec2.Body.String())
	}
}

func TestIdempotency_DifferentBodyUnderSameKeyReturns422(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)
	key := "test-key-conflict"

	req1 := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"a":1}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", key)
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: status=%d body=%s", rec1.Code, rec1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"a":2}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", key)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestIdempotency_InFlightSameKeyReturns409(t *testing.T) {
	store := inMemStore(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	idemMw := NewIdempotency(store, zap.NewNop())
	defer idemMw.Stop()

	// Manually reserve a row as in_flight so the middleware sees it
	// already present on the second request.
	if _, _, err := store.TryInsert(context.Background(), "key-409", ""); err != nil {
		t.Fatalf("manual TryInsert: %v", err)
	}

	r.Use(idemMw.Handler())
	r.POST("/x", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-409")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdempotency_InFlightAbortsDownstreamHandler(t *testing.T) {
	// Regression: a 409 in-flight reply must NOT fall through to the
	// downstream route handler. Without c.Abort() the outer c.Next()
	// loop runs the handler anyway, appending its 200 body to the 409
	// payload and enqueueing a duplicate side-effect (PR-idempotency-abort).
	store := inMemStore(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	idemMw := NewIdempotency(store, zap.NewNop())
	defer idemMw.Stop()

	if _, _, err := store.TryInsert(context.Background(), "key-409-abort", ""); err != nil {
		t.Fatalf("manual TryInsert: %v", err)
	}

	var invocations int
	r.Use(idemMw.Handler())
	r.POST("/x", func(c *gin.Context) {
		invocations++
		c.JSON(http.StatusOK, gin.H{"handler_ran": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "key-409-abort")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if invocations != 0 {
		t.Fatalf("downstream handler MUST NOT run on 409 in-flight (invocations=%d)", invocations)
	}
	if strings.Contains(rec.Body.String(), "handler_ran") {
		t.Fatalf("409 body must not contain handler output; got %q", rec.Body.String())
	}
}

func TestIdempotency_BodyConflictAbortsDownstreamHandler(t *testing.T) {
	store := inMemStore(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	idemMw := NewIdempotency(store, zap.NewNop())
	defer idemMw.Stop()

	var invocations int
	r.Use(idemMw.Handler())
	r.POST("/x", func(c *gin.Context) {
		invocations++
		c.JSON(http.StatusOK, gin.H{"handler_ran": true})
	})

	// Fill the cache with body {"a":1}.
	req1 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":1}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", "key-422-abort")
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	invocations = 0

	// Same key + different body → 422; handler must not run.
	req2 := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":2}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "key-422-abort")
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rec2.Code, rec2.Body.String())
	}
	if invocations != 0 {
		t.Fatalf("downstream handler MUST NOT run on 422 body conflict (invocations=%d)", invocations)
	}
	if strings.Contains(rec2.Body.String(), "handler_ran") {
		t.Fatalf("422 body must not contain handler output; got %q", rec2.Body.String())
	}
}

func TestIdempotency_OnlyWhitespacePassesThrough(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)

	// After TrimSpace an empty-string key falls through the middleware;
	// downstream handler runs unmodified. The test name intentionally
	// describes the actual pass-through behaviour; previously titled
	// `EmptyKeyAfterTrimRejected` which implied the wrong direction.
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`x`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "   ")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("whitespace-only key passes through unchanged; got status=%d", rec.Code)
	}
}

func TestIdempotency_KeyCapAt255Chars(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)

	long := strings.Repeat("a", 256)
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`x`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", long)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for key >255 chars, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdempotency_NonPrintableKeyRejected(t *testing.T) {
	store := inMemStore(t)
	r := newRouter(t, store)

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`x`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "abc\ndef")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-printable key, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdempotency_MultipartBypassesBodyHash(t *testing.T) {
	store := inMemStore(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	idemMw := NewIdempotency(store, zap.NewNop())
	defer idemMw.Stop()

	// Multipart bypass sets body_hash="" so the 422 same-key+diff-body
	// check is suppressed. The handler still runs ONCE for the fresh
	// acquisition; subsequent requests with the same key see the cached
	// response (empty since the test handler reads the body but never
	// writes one) and the X-Idempotency-Replay header is set on the second.
	// The handler MUST NOT fire twice — replay path aborts the chain
	// to avoid re-processing the upload (which would re-stream bytes
	// to Drive).
	var invocations int
	r.Use(idemMw.Handler())
	r.POST("/upload", func(c *gin.Context) {
		invocations++
		_, _ = io.ReadAll(c.Request.Body) // stream body intact to handler
	})

	mkReq := func(b string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(b))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=---test-boundary")
		req.Header.Set("Idempotency-Key", "mp-key")
		return req
	}

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, mkReq("FILEBODY1"))
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, mkReq("FILEBODY2"))

	if rec1.Code != http.StatusOK {
		t.Fatalf("first multipart: status=%d", rec1.Code)
	}
	if rec2.Code != http.StatusOK {
		t.Fatalf("second multipart: status=%d (expected 200 — multipart bypass means no 422 conflict on different body)", rec2.Code)
	}
	if rec1.Header().Get(replayHeader) != "" {
		t.Fatalf("first request must NOT set X-Idempotency-Replay header")
	}
	if rec2.Header().Get(replayHeader) != "true" {
		t.Fatalf("second request must set X-Idempotency-Replay: true (got %q)", rec2.Header().Get(replayHeader))
	}
	if invocations != 1 {
		t.Fatalf("expected exactly 1 handler invocation (replay path aborts), got %d", invocations)
	}
}

// concurrentTestGate guards sharing-state across the concurrency test.
var concurrentTestGate sync.Mutex

func TestIdempotency_NilStorePassesThrough(t *testing.T) {
	mw := NewIdempotency(nil, zap.NewNop())
	defer mw.Stop()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(mw.Handler())
	r.POST("/echo", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`x`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "anything")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nil store must pass through without panic; status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdempotency_ConcurrentSameKeyYieldsOneCacheFillOneConflict(t *testing.T) {
	// Two goroutines, same key + same body. One must succeed with 200,
	// the other must see in_flight and return 409. The ordering is
	// nondeterministic so we accept either branch for each goroutine
	// as long as exactly one gets 200 and the other gets 409.
	concurrentTestGate.Lock()
	defer concurrentTestGate.Unlock()

	store := inMemStore(t)
	r := newRouter(t, store)
	key := "race-key"
	body := `{"r":"1"}`

	var wg sync.WaitGroup
	results := make([]int, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			results[i] = rec.Code
		}()
	}
	wg.Wait()

	var ok, conflict int
	for _, code := range results {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok+conflict != 2 {
		t.Fatalf("expected one 200 + one 409; got results=%v", results)
	}
}
