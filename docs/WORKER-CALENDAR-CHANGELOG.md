# Worker Calendar contract changelog

The canonical protocol document is mirrored in the InstaEdit and PipelineGen
repositories as `docs/WORKER-CALENDAR-EVENTS.md`.

## v1.1.0

- Added a disk-backed progress outbox; enqueue is fsynced and independent of
  network availability, while a background drainer retries transient failures.
- The Job Master worker reports progress, lease heartbeats, terminal status,
  and structured errors automatically.
- Added owner-scoped M2M job cancellation and Calendar cancellation signals.
- The video.create delivery stage re-reads the Calendar schedule before upload
  and observes later reschedules while waiting.
- Calendar event creation now uses a native PostgreSQL `ON CONFLICT` upsert.

## v1.0.1

- Worker progress clients serialize structured failures as `error_code`,
  `reason`, and optional `output_tail`.
- Reporting uses three total attempts for transport errors, HTTP 429, and 5xx,
  with exponential backoff and jitter.

## v1.0.0

- Added workspace-scoped idempotent Calendar event creation, editing,
  deletion, and per-kind progress reporting.
- Added worker heartbeat timestamps and a server-side 15-minute stale-event
  sweep.
