# Operational Audit: PipelineGen Backend Infrastructure

This document outlines the professional architectural patterns, maintenance routines, and observability strategies implemented in the PipelineGen backend.

## 1. High-Performance Logging Architecture

The system uses an asynchronous, non-blocking producer-consumer pattern for API request logging to ensure zero latency on the critical request path.

### Flow Diagram
```text
[HTTP Request] -> [Logger Middleware] -> [Channel (buf: 5000)] -> [Batcher] -> [SQLite WAL]
                     |                                           |
                     +--> (If full) -> [Drop & Counter]         +--> (Flush: 100ms or 200 items)
```

### Key Technical Specs:
- **Zero Latenza**: Request logging happens on a separate goroutine. If the 5,000-item buffer is full, logs are dropped and an atomic counter is incremented instead of blocking the request.
- **Batching**: Database writes are batched into transactions (max 200 items or 100ms) to minimize disk I/O.
- **SQLite WAL Mode**: Optimized for concurrent reads and writes.
- **Graceful Shutdown**: Upon `SIGTERM` or `SIGINT`, the system flushes the log channel and waits for the writer to finish before exiting.

## 2. Maintenance & Data Retention

The `MaintenanceService` handles automated cleanup tasks to keep the system lean and performant.

### Log Retention
- **Policy**: API request logs are kept for **30 days** (configurable via `VELOX_RETENTION_DAYS`).
- **Pruning**: A daily job deletes old logs: `DELETE FROM api_requests WHERE ts < datetime('now', '-30 days')`.
- **Space Reclamation**: After pruning, the system runs `PRAGMA incremental_vacuum` to reclaim disk space without locking the entire database.

### Storage Optimization
- **Orphan Cleanup**: Scans the `assets/subjects` directory and removes files not referenced in the `asset_index`.
- **WAL Checkpointing**: SQLite automatically handles checkpointing, but systemd's `LimitNOFILE=65536` ensures headroom for open file descriptors during peaks.

## 3. Observability & Debugging

### Key Metrics (Internal)
- `pipelinegen_log_dropped_total`: Total logs dropped due to backpressure.
- `pipelinegen_db_size_bytes`: Current size of the SQLite main database.

### Systemd Operations
```bash
# Check service status and logs
systemctl status pipelinegen
journalctl -u pipelinegen -f

# Force a manual maintenance run
curl -X POST http://localhost:8080/api/system/cleanup?deep=true
```

### SQL Debugging Queries
```sql
-- Find slow endpoints (> 500ms)
SELECT path, method, duration_ms 
FROM api_requests 
WHERE duration_ms > 500 
ORDER BY duration_ms DESC LIMIT 10;

-- Errors by path in the last hour
SELECT path, count(*) as err_count 
FROM api_requests 
WHERE status >= 400 AND ts > datetime('now', '-1 hour') 
GROUP BY path;

-- Check WAL size and status
PRAGMA wal_checkpoint(PASSIVE);
```

## 4. Resilience Targets

- **Restart Policy**: `on-failure` with a 2-second delay.
- **Backpressure Threshold**: 5,000 concurrent log entries.
- **Shutdown Wait**: Up to 5 seconds for log flushing.

---
*Created on: 2026-05-18*
