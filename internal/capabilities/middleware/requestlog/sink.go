// Package requestlog owns the canonical types for persistent HTTP
// request logging. middleware and the SQLite-backed logsink adapter
// both import this package; placing the types in
// internal/application/middleware/requestlog keeps the dependency
// direction canonical per AGENTS.md Pattern 0 (infrastructure
// implements ports declared in application, never the reverse).
package middleware

import (
	"context"
	"time"
)

// RequestLogEntry is the canonical, type-safe shape of one persisted
// HTTP request record. The values mirror the columns of the
// api_requests SQLite table but expose them as Go types so the
// middleware layer no longer touches *sql.DB. Composition injects a
// SQLite-backed implementation; the application/test layers build
// fakes against the same surface.
type RequestLogEntry struct {
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

// RequestLogSink is the canonical port for persistent HTTP request
// logging. The middleware hands each completed request to Log and
// batches via FlushBatch to match SQLite transaction throughput.
// The composition root injects a concrete SQLite-backed adapter;
// the API layer never holds *sql.DB.
type RequestLogSink interface {
	// Log writes a single entry. The middleware is non-blocking, so
	// the sink is expected to either buffer or hand off to a
	// background worker; synchronous implementations are allowed but
	// must not block long enough to backpressure request handling.
	Log(ctx context.Context, entry RequestLogEntry) error

	// FlushBatch persists a batch of entries in a single transaction.
	// Returns nil on success; the adapter handles per-entry errors by
	// logging them inside the transaction rather than aborting the
	// whole batch (preserving the original middleware_logger.go
	// behavior where stmt.Exec failures were logged-and-skipped).
	FlushBatch(ctx context.Context, batch []RequestLogEntry) error

	// Stop drains pending entries and shuts down any background
	// workers. Idempotent — second call is a no-op.
	Stop(ctx context.Context) error
}
