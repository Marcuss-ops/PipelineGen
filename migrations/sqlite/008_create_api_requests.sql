-- Migration: 008_create_api_requests.sql
-- Description: Adds a table to store API request logs for audit and performance monitoring.

CREATE TABLE IF NOT EXISTS api_requests (
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

CREATE INDEX IF NOT EXISTS idx_api_requests_ts ON api_requests(ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_path_status ON api_requests(path, status);
CREATE INDEX IF NOT EXISTS idx_api_requests_user ON api_requests(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_request_id ON api_requests(request_id);
