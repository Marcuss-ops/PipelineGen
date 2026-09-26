# Worker-created Calendar video events

Workers may reserve Calendar cards before processing starts, then report each
execution kind directly as it advances. Requests use the normal workspace API
key with the `automation` permission. The workspace always comes from the
authenticated key; it is never accepted from the request body.

## Create up to 20 video events

`POST /api/v1/agent/calendar/events/batch`

```json
{
  "events": [
    {
      "event_key": "dolly-parton-video-01",
      "title": "Dolly Parton: early years",
      "scheduled_at": "2026-10-01T16:00:00Z",
      "kind": "script.generate",
      "job_id": "job_123"
    }
  ]
}
```

The batch accepts 1–20 unique event keys. `scheduled_at` is optional; when
omitted the card is placed on the current day. Retrying an identical request
returns the same post. Reusing a key with different event data returns `409`.
Each item becomes a draft post with no publication targets, so creating a
worker event cannot enqueue a social upload.

`job_id` is optional and is retained in the event metadata for correlation. Worker IDs are stored in `worker_remote_job_id` and echoed in every Calendar diagnostic snapshot.

## Edit or delete a draft event

`PATCH /api/v1/agent/calendar/events/{event_key}` accepts either or both of
`title` and RFC3339 `scheduled_at`. It is scoped to the key's workspace and
only edits worker-created draft cards.

`DELETE /api/v1/agent/calendar/events/{event_key}` deletes a worker-created
draft card that has no publication targets. This removes the Calendar card;
it does not cancel a remote execution-plane job already submitted by a worker; cancel control is a follow-up protocol and is not yet wired.

## Report kind progress

`PATCH /api/v1/agent/calendar/events/{event_key}/progress`

```json
{
  "kind": "media.stock",
  "status": "RUNNING",
  "progress": 40,
  "phase": "stock_download",
  "snapshot": {
    "events": [{"type": "asset.ready", "message": "Stock asset downloaded"}]
  }
}
```

Kinds: `script.generate`, `media.stock`, `youtube_clip.extract`,
`voiceover.generate`, `image.generate.google`, `clip.render`, `video.assemble`,
and `video.create`. Statuses: `QUEUED`, `RUNNING`, `SUCCEEDED`, `COMPLETED`,
`FAILED`, and `CANCELLED`. Progress is optional and ranges from 0 to 100.
Updates are scoped to the API key's workspace and a worker-created event key;
per-kind snapshots are retained in the Calendar card so concurrent stages do
not erase one another. `RUNNING` updates write `worker_heartbeat_at`. A worker
that sends `FAILED` must include `error: {"error_code":"...", "reason":"...",
"output_tail":"..."}`; the sweep marks jobs with a heartbeat older than
15 minutes as `FAILED` / `TIMEOUT`.
