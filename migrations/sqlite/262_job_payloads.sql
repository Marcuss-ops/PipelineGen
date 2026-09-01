-- database: primary
-- Durable job request payloads live outside the hot queue row.
CREATE TABLE IF NOT EXISTS job_payloads (
    job_id TEXT PRIMARY KEY,
    codec_id TEXT NOT NULL DEFAULT 'json',
    payload TEXT NOT NULL DEFAULT '{}',
    payload_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

INSERT OR IGNORE INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
SELECT id, 'json', COALESCE(payload_json, '{}'), '',
       strftime('%Y-%m-%dT%H:%M:%fZ','now')
FROM jobs
WHERE COALESCE(payload_json, '') NOT IN ('', 'null');
