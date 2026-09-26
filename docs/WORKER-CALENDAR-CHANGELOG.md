# Worker Calendar contract changelog

The canonical protocol document is mirrored in the InstaEdit and PipelineGen
repositories as `docs/WORKER-CALENDAR-EVENTS.md`.

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
