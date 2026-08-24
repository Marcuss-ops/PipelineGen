-- 095_create_idempotency_keys.sql
--
-- CREATES the canonical idempotency_keys table backing the reusable
-- Gin idempotency middleware (PR8, file: internal/api/middleware/idempotency.go).
--
-- Why: before PR8 every write handler that wanted replay safety rolled its
-- own ad-hoc Idempotency-Key extraction (e.g. generate_batch_usecase.go
-- prefixes the key as `idem:<key>` to dedup async job enqueue, but the
-- underlying POST was not cached so a network-level retry generated a
-- SECOND job). The middleware here combines:
--   - opaque `key` (Stripe-style, up to 255 chars) extracted from
--     `Idempotency-Key` header — primary key on the table for INSERT-or-FAIL
--     atomicity under the same connection.
--   - `body_hash` (URL-safe hex SHA-256 over the canonical request body;
--     "" for multipart where the body is buffered) used to detect the
--     "same key + different body" reuse → 422 Unprocessable Entity.
--   - `status` (`in_flight` | `completed`) partitioning acquisition from
--     completion. Middleware INSERTs as `in_flight` BEFORE invoking the
--     downstream handler, then UPDATEs to `completed` + response payload
--     after the handler returns. A second concurrent request with the
--     same key finds the row already present and gets a 409 Conflict.
--   - `response_status`, `response_body`, `response_content_type` form the
--     replay payload. Headers are dumped as a JSON text blob (Content-Type
--     is the only header the replay must reproduce; the rest are
--     derivable from the body).
--   - `created_at` (RFC3339) + `expires_at` (RFC3339 + 24h) drive the
--     absolute-24h TTL. A `time.Ticker` goroutine inside the middleware
--     runs `DELETE WHERE expires_at < now` every 15 minutes.
--
-- Companion code:
--   internal/application/middleware/idempotency_store.go — store port
--   internal/platform/sqlite/idempotency/repository.go —
--     concrete Repository using this table (TryInsert / Complete /
--     Get / DeleteExpired)
--   internal/api/middleware/idempotency.go — Gin middleware; reads
--     the port via the handler's compose-root wiring
--
-- Idempotent: IF NOT EXISTS everywhere. Re-applying on a database that
-- has the table from ad-hoc bootstrapping is a no-op. Verified after
-- migration by `PRAGMA table_info(idempotency_keys)` matching the
-- INSERT projection in Repository.TryInsert.
--
-- Column ordering follows the canonical INSERT projection in
-- Repository.TryInsert for diff-by-eye alignment between schema and
-- the Go application code.

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key TEXT PRIMARY KEY,                       -- opaque 255-char maximum, PRIMARY KEY enables INSERT-or-FAIL atomicity
    body_hash TEXT NOT NULL DEFAULT '',         -- URL-safe hex SHA-256 of request body; empty for multipart bypass
    status TEXT NOT NULL DEFAULT 'in_flight',   -- 'in_flight' | 'completed'
    response_status INTEGER NOT NULL DEFAULT 0, -- HTTP status from the cached response (0 while in_flight)
    response_body TEXT NOT NULL DEFAULT '',     -- raw response body bytes (may be JSON, may be empty)
    response_content_type TEXT NOT NULL DEFAULT '', -- Content-Type header from the cached response
    created_at TEXT NOT NULL,                   -- RFC3339 timestamp at TryInsert
    expires_at TEXT NOT NULL,                   -- RFC3339 timestamp at TryInsert + 24h (absolute TTL)
    last_replayed_at TEXT NOT NULL DEFAULT ''   -- RFC3339 timestamp of the most recent replay (audit)
);

-- Required by Repository.DeleteExpired's `WHERE expires_at < ?` scan;
-- also speeds up the cleanup ticker's range scans.
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at
    ON idempotency_keys(expires_at);
