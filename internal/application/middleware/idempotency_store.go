// Package middleware (ports) — IdempotencyStore port for the
// PR8 reusable Gin idempotency middleware.
//
// The port is the canonical narrow surface of the SQLite-backed
// idempotency_keys table (migration 095) used by the API middleware
// to implement the Idempotency-Key + body-hash + 24h-replay
// pattern across all write handlers. Per AGENTS.md Pattern 0 the
// port is defined here and the concrete SQLite adapter lives in
// internal/platform/sqlite/idempotency. The Gin
// middleware in internal/api/middleware/idempotency.go consumes
// only this port, never a concrete repo (compile-time assertion
// below pins the contract).
//
// Lifecycle:
//   - TryInsert  INSERTs a new `in_flight` row keyed on `key`. Returns
//     AlreadyExists=true when the row already exists (caller path
//     determines whether the existing row is a 409 in-flight or a
//     replayable 200/4xx response).
//   - Complete   UPDATEs the `in_flight` row to `completed` with the
//     captured response payload. Called by the middleware on a
//     successful downstream handler return.
//   - Get        Returns the current row, used by the middleware to
//     decide between replay (status == 'completed') and conflict
//     (status == 'in_flight', 409) paths.
//   - DeleteExpired deletes all rows with expires_at < NOW. Called
//     by the cleanup ticker (every 15 minutes) to enforce the
//     24h absolute TTL.
//
// Design notes:
//   - opaque-string keys up to 255 chars (Stripe-style) — the
//     port does NOT validate shape; that responsibility lives
//     with the middleware so non-RFC4122 client keys (e.g.
//     `idem_vid_99342` deterministic hashes) still work.
//   - body_hash is opaque too (may be empty for multipart bypass).
//   - TTL is absolute (created_at + 24h), NOT sliding — matches
//     Stripe documented behavior and bounds storage.
package middleware

import (
	"context"
	"time"
)

// IdempotencyRecord captures the state of one Idempotency-Key
// entry in the store. Fields are exported so the middleware can
// inspect both the status and the cached response payload without
// re-querying.
type IdempotencyRecord struct {
	Key            string
	BodyHash       string
	Status         string // "in_flight" | "completed"
	ResponseStatus int
	ResponseBody   []byte
	ResponseCT     string // Content-Type
	CreatedAt      time.Time
	ExpiresAt      time.Time
	LastReplayedAt time.Time
}

// IdempotencyStore is the canonical port consumed by the Gin
// idempotency middleware. The middleware imports only this port;
// the concrete SQLite-backed implementation lives in
// internal/platform/sqlite/idempotency.
//
// Implementations MUST:
//   - atomic TryInsert with PRIMARY KEY constraint (SQLite "INSERT
//     OR FAIL" or callers' UPSERT dance); returning AlreadyExists=true
//     when the row exists.
//   - tolerate body_hash="" for multipart bypass.
//   - format RFC3339 timestamps consistently (time.RFC3339Nano or
//     time.RFC3339 but pick one and stick to it).
type IdempotencyStore interface {
	// TryInsert atomically inserts a new in_flight row for `key`.
	// Returns:
	//   (nil, AlreadyExists=true, nil)  if a row with that key already exists.
	//   (record, false, nil)             on successful insertion.
	//   (nil, false, err)                on storage error.
	TryInsert(ctx context.Context, key, bodyHash string) (record *IdempotencyRecord, alreadyExists bool, err error)

	// Complete transitions the row for `key` from in_flight to
	// completed, attaching the captured response payload. Returns
	// ErrIdempotencyKeyNotInFlight if the row is not in_flight
	// (handle this gracefully — the handler may have raced).
	Complete(ctx context.Context, key string, responseStatus int, responseBody []byte, responseContentType string) error

	// Get returns the current row for `key`. Returns
	// ErrIdempotencyKeyNotFound when no row exists.
	Get(ctx context.Context, key string) (*IdempotencyRecord, error)

	// DeleteExpired deletes all rows with expires_at < now.
	// Returns the count of rows deleted. Called by the
	// middleware's background cleanup ticker.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}

// ErrIdempotencyKeyNotFound is returned by Get when no row exists
// for the requested key. The middleware treats this as "no replay
// candidate" and continues to the downstream handler.
var ErrIdempotencyKeyNotFound = errIdempotencyKeyNotFound("idempotency: key not found")

// ErrIdempotencyKeyNotInFlight is returned by Complete when the row
// exists but is not in_flight (e.g. status == 'completed' after a
// prior call). The middleware logs and ignores — this can happen if
// the cleanup ticker prematurely expires a row that just completed.
var ErrIdempotencyKeyNotInFlight = errIdempotencyKeyNotInFlight("idempotency: key not in flight")

type errIdempotencyKeyNotFound string

func (e errIdempotencyKeyNotFound) Error() string { return string(e) }

type errIdempotencyKeyNotInFlight string

func (e errIdempotencyKeyNotInFlight) Error() string { return string(e) }
