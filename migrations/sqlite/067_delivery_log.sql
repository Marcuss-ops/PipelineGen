-- 067: delivery_log — audit trail for delivery.requested outbox events.
--
-- The delivery handler in internal/application/jobs/outbox/delivery.go writes
-- one row per HTTP POST attempt keyed by delivery_id (the producer-supplied
-- dedupe key, mirrored from the outbox event_key). Re-deliveries of the same
-- delivery_id update the same row via ON CONFLICT(delivery_id) DO UPDATE —
-- idempotent at the audit-trail level too.
--
-- Columns:
--   id            autoincrement primary key (debug / sort)
--   asset_id      the canonical PipelineGen asset id that was delivered
--   endpoint_url  the receiver URL the handler POSTed to
--   delivery_id   producer-supplied dedupe key (UNIQUE)
--   status_code   last HTTP status observed (NULL ⇒ network failure)
--   response_hash SHA-256 hex of the truncated response body (NULL when no body)
--   delivered_at  RFC3339 timestamp of the most-recent attempt
--   created_at    RFC3339 timestamp of the first attempt (immutable after dedupe update)
--
-- Idempotent so the migration can be re-run as part of the post-deploy
-- gate (`make run --mode all` over an existing database) without erroring.
-- delivery_id is the canonical dedupe key. Inline UNIQUE makes the column
-- targetable by `INSERT ... ON CONFLICT(delivery_id) DO UPDATE` on every
-- SQLite version; a separate CREATE UNIQUE INDEX is NOT enough for the
-- column-based ON CONFLICT clause on all driver builds.
CREATE TABLE IF NOT EXISTS delivery_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_id      TEXT NOT NULL,
  endpoint_url  TEXT NOT NULL,
  delivery_id   TEXT NOT NULL UNIQUE,
  status_code   INTEGER,
  response_hash TEXT,
  delivered_at  TEXT,
  created_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_delivery_log_asset_id
  ON delivery_log (asset_id);

CREATE INDEX IF NOT EXISTS idx_delivery_log_delivered_at
  ON delivery_log (delivered_at);
