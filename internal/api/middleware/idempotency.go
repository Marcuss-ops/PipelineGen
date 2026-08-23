// Package middleware — Idempotency is the reusable Gin idempotency
// middleware (PR8, June 2026).
//
// The middleware implements the canonical Stripe-style Idempotency-Key
// pattern:
//
//		POST /api/media/clip_create  (Idempotency-Key: <opaque-key>)
//
//	 1. Extract the key from the request header. If absent, fall through
//	    to the downstream handler unchanged (the handler is free to do its
//	    own dedup). Length-capped at 255 chars and validated as
//	    printable-ASCII; malformed keys return 400 BadRequest.
//	 2. Hash the request body (SHA-256 over the raw bytes read off
//	    c.Request.Body). For multipart uploads the body is streamed
//	    directly to the artifact service — buffering a 500MB file in
//	    memory defeats that — so the body_hash is set to "" and the
//	    middleware falls through to replay-only mode (no body-conflict
//	    422, just in-flight 409 and completed-state cache).
//	 3. Try to acquire the key (INSERT in_flight):
//	    - row freshly inserted: install a wrapper ResponseWriter that
//	      tees the response body into a buffer. On handler completion,
//	      UPDATE the row to completed + response payload (Complete).
//	    - row already present: load it.
//	    - status == 'completed' and body_hash matches: replay the cached
//	      response verbatim, set X-Idempotency-Replay: true.
//	    - status == 'completed' and body_hash differs: 422
//	      Unprocessable Entity (same key reused with different body).
//	    - status == 'in_flight': 409 Conflict (concurrent request).
//
// Background goroutine: a time.Ticker every 15 minutes calls
// DeleteExpired to garbage-collect rows past their 24h TTL. Stop()
// stops the ticker.
//
// Reference: migration 095_create_idempotency_keys.sql,
// internal/application/middleware/idempotency_store.go (port),
// internal/infrastructure/database/sqlite/idempotency (concrete).
package middleware

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mw "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
)

// idempotencyHeader is the canonical header name (RFC draft + Stripe convention).
const idempotencyHeader = "Idempotency-Key"

// replayHeader is set on cached response replays so clients can detect them.
const replayHeader = "X-Idempotency-Replay"

// maxKeyLen matches the port's enforced cap (255 chars).
const maxKeyLen = 255

// cleanupInterval is the background DeleteExpired tick cadence.
const cleanupInterval = 15 * time.Minute

// Idempotency is the canonical middleware handle. Construct via
// NewIdempotency; the returned wrapper exposes a gin.HandlerFunc
// and a Stop() method to halt the cleanup goroutine.
//
// PG-008 (June 2026): the middleware is constructed explicitly per
// consumer (not via a global setter) so the lifecycle is owned by
// the request scope rather than the process scope. The cleanup
// goroutine is the only long-lived piece and is owned by the
// returned *Idempotency value.
type Idempotency struct {
	handler gin.HandlerFunc
	stopCh  chan struct{}
	stopOne sync.Once
}

// Stop halts the background cleanup ticker. Idempotent.
func (i *Idempotency) Stop() {
	if i == nil {
		return
	}
	i.stopOne.Do(func() {
		close(i.stopCh)
	})
}

// Handler returns the gin.HandlerFunc to install in the route
// chain.
func (i *Idempotency) Handler() gin.HandlerFunc {
	if i == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return i.handler
}

// NewIdempotency creates the canonical middleware. The store MUST
// be the concrete IdempotencyStore adapter (middleware.go does
// not itself depend on infrastructure/ — Pattern 0 boundary).
// A nil store returns a no-op middleware (Pass-through) so test
// fixtures and dry-run CLI invocations don't panic.
//
// The cleanup goroutine starts immediately when store != nil.
// Stop() on shutdown releases the ticker. stopCh is always
// allocated so Stop() is safe to call on a nil-store instance
// (closes the channel harmlessly; the goroutine was never started).
func NewIdempotency(store mw.IdempotencyStore, log *zap.Logger) *Idempotency {
	stopCh := make(chan struct{})
	v := &Idempotency{
		handler: func(c *gin.Context) { c.Next() },
		stopCh:  stopCh,
	}
	if store == nil {
		// no-op pass-through; no cleanup goroutine started.
		return v
	}
	if log == nil {
		log = zap.NewNop()
	}
	v.handler = buildMiddleware(store, log)
	go cleanupLoop(store, stopCh, log)
	return v
}

// buildMiddleware wires the gin.HandlerFunc. The handler is the
// only piece that sees gin.Context — the upstream store is
// transport-agnostic.
func buildMiddleware(store mw.IdempotencyStore, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader(idempotencyHeader))
		if key == "" {
			// No key supplied — pass through unchanged.
			c.Next()
			return
		}
		if !isPrintableASCII(key) {
			apiutilError(c, http.StatusBadRequest,
				"Idempotency-Key must contain only printable ASCII characters")
			return
		}
		if len(key) > maxKeyLen {
			apiutilError(c, http.StatusBadRequest,
				"Idempotency-Key exceeds 255 characters")
			return
		}

		ct := c.ContentType()
		isMultipart := strings.HasPrefix(ct, "multipart/form-data")

		// Body hash. Multipart bypass — empty hash = no body-conflict check.
		var bodyHash string
		if !isMultipart {
			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				apiutilError(c, http.StatusBadRequest,
					"failed to read request body for idempotency hashing: "+err.Error())
				return
			}
			// Reset Body so the downstream handler can re-read it.
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			sum := digest.SHA256Bytes(body)
			bodyHash = hex.EncodeToString([]byte(sum))
		}

		ctx := c.Request.Context()
		rec, dup, err := store.TryInsert(ctx, key, bodyHash)
		if err != nil {
			log.Error("idempotency.TryInsert failed", zap.String("key", key), zap.Error(err))
			apiutilError(c, http.StatusInternalServerError,
				"idempotency store unavailable")
			return
		}
		if !dup {
			// Fresh acquisition — install the response tee and run
			// the downstream handler. On return, persist the response.
			tee := newTeeingResponseWriter(c.Writer)
			c.Writer = tee
			c.Next()

			// After the handler completes, persist the captured response.
			// Errors here are logged but never block the return path;
			// the client has already received the response.
			if err := store.Complete(ctx, key,
				tee.statusCode, tee.body.Bytes(), tee.contentType); err != nil {
				if !errors.Is(err, mw.ErrIdempotencyKeyNotInFlight) {
					log.Warn("idempotency.Complete failed",
						zap.String("key", key), zap.Error(err))
				}
			}
			return
		}

		// Row already existed — load it and decide replay vs conflict.
		existing, gerr := store.Get(ctx, key)
		if gerr != nil {
			log.Error("idempotency.Get after collision failed",
				zap.String("key", key), zap.Error(gerr))
			apiutilError(c, http.StatusInternalServerError,
				"idempotency store unavailable")
			return
		}

		if existing.Status == "in_flight" {
			apiutilError(c, http.StatusConflict,
				"Idempotency-Key request already in flight")
			return
		}

		// status == 'completed'
		if !isMultipart && existing.BodyHash != "" && existing.BodyHash != bodyHash {
			// Per PR8 reviewer fix C: do not leak the original body_hash
			// (a request fingerprint) to potential adversaries; the hash
			// is logged server-side in the warn line below for ops
			// debugging.
			log.Warn("idempotency body-hash conflict (key reused with different body)",
				zap.String("key", key),
				zap.String("expected_body_hash", existing.BodyHash))
			apiutilError(c, http.StatusUnprocessableEntity,
				"Idempotency-Key reused with different request body")
			return
		}

		// Replay the cached response. We MUST return without invoking
		// c.Next() — otherwise the downstream handler runs again and
		// would mutate external state on the "replay" request, defeating
		// the entire point of cached replay. `return` alone suffices
		// (we're at the end of the closure), but we also call c.Abort
		// so any subsequent Gin middleware in the chain (404 handler,
		// logger, etc.) sees an aborted request and short-circuits.
		c.Writer.Header().Set(replayHeader, "true")
		if existing.ResponseCT != "" {
			c.Writer.Header().Set("Content-Type", existing.ResponseCT)
		}
		c.Writer.WriteHeader(existing.ResponseStatus)
		if len(existing.ResponseBody) > 0 {
			_, _ = c.Writer.Write(existing.ResponseBody)
		}
		c.Abort()
		log.Debug("idempotency replay",
			zap.String("key", key),
			zap.Int("response_status", existing.ResponseStatus))
		_ = rec // silence unused in transit
	}
}

// teeResponseWriter implements gin.ResponseWriter by writing the
// downstream bytes through and capturing them into bodyB. The
// `Hijack` / `CloseNotify` / `Flush` set is the gin.ResponseWriter
// surface subset we need to honour; the rest are no-ops because
// gin never calls them.
type teeResponseWriter struct {
	gin.ResponseWriter
	body        *bytes.Buffer
	statusCode  int
	contentType string
	wroteHeader bool
}

func newTeeingResponseWriter(w gin.ResponseWriter) *teeResponseWriter {
	return &teeResponseWriter{
		ResponseWriter: w,
		body:           &bytes.Buffer{},
		statusCode:     http.StatusOK,
	}
}

func (t *teeResponseWriter) WriteHeader(code int) {
	t.statusCode = code
	if ct := t.ResponseWriter.Header().Get("Content-Type"); ct != "" {
		t.contentType = ct
	}
	t.ResponseWriter.WriteHeader(code)
	t.wroteHeader = true
}

func (t *teeResponseWriter) Write(b []byte) (int, error) {
	if !t.wroteHeader {
		// Default to 200 if the handler forgets to call WriteHeader.
		t.statusCode = http.StatusOK
		if ct := t.ResponseWriter.Header().Get("Content-Type"); ct != "" {
			t.contentType = ct
		}
		t.wroteHeader = true
	}
	t.body.Write(b) // capture
	return t.ResponseWriter.Write(b)
}

func (t *teeResponseWriter) WriteString(s string) (int, error) {
	if !t.wroteHeader {
		t.statusCode = http.StatusOK
		if ct := t.ResponseWriter.Header().Get("Content-Type"); ct != "" {
			t.contentType = ct
		}
		t.wroteHeader = true
	}
	t.body.WriteString(s)
	return t.ResponseWriter.WriteString(s)
}

// cleanupLoop runs DeleteExpired every cleanupInterval until stopCh closes.
func cleanupLoop(store mw.IdempotencyStore, stopCh <-chan struct{}, log *zap.Logger) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			n, err := store.DeleteExpired(ctx, time.Now())
			cancel()
			if err != nil {
				log.Warn("idempotency cleanup failed", zap.Error(err))
				continue
			}
			if n > 0 {
				log.Info("idempotency cleanup expired rows",
					zap.Int("deleted", n))
			}
		}
	}
}

// isPrintableASCII rejects whitespace and control characters
// which would make keys hard to debug and could leak newlines
// into log queries.
func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return false
		}
	}
	return true
}

// apiutilError is a thin local helper so this package doesn't pull
// pkg/apiutil (which would lift gin dependencies onto the leaf
// utility package). Mirrors apiutil.Error.
//
// It aborts the gin chain after writing the error body. Without
// c.Abort(), returning from a middleware skips only the handlers
// already run by a nested c.Next(); the outer c.Next() loop continues
// to the next handler, so the downstream route handler would still
// execute and append its own response to the error body (e.g. a 409
// in-flight reply followed by the handler's 200 "enqueued" payload,
// and a duplicate job).
func apiutilError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"ok": false, "error": msg})
	c.Abort()
}
