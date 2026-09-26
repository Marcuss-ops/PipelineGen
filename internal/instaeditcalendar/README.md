# InstaeditScheduling

Standalone Go client/CLI for worker 77. Configure `INSTAEDIT_URL` (defaults to `https://api.instaedit.org`) and `INSTAEDIT_API_KEY` in the worker service secret environment. The key must be workspace-scoped and have the `automation` permission. Never commit a real key.

Create up to 20 calendar events with a JSON file:

```json
{"events":[{"event_key":"worker77-video-001","title":"Dolly Parton - 01","kind":"script.generate"}]}
```

```sh
go run ./cmd/instaedit-scheduling create-batch batch.json
```

Report status for each pipeline kind with a JSON file:

```json
{"kind":"script.generate","status":"RUNNING","progress":25,"phase":"writing_script","snapshot":{"job_id":"worker-local-123"}}
```

```sh
go run ./cmd/instaedit-scheduling progress worker77-video-001 progress.json
```

Supported kinds: `script.generate`, `media.stock`, `youtube_clip.extract`, `voiceover.generate`, `image.generate.google`, `clip.render`, `video.assemble`, `video.create`. Supported statuses: `QUEUED`, `RUNNING`, `SUCCEEDED`, `COMPLETED`, `FAILED`, `CANCELLED`.

Creating these events creates Calendar draft cards and does not itself dispatch or execute a pipeline job. Retain each `event_key` in the worker's local job record and PATCH it as each job kind advances.

## 20-video smoke payload

A ready-to-submit 20-event payload is in `examples/dolly-parton-20.json`. Submit it after the worker service has the API key configured:

```sh
go run ./cmd/instaedit-scheduling create-batch examples/dolly-parton-20.json
```

## Edit or delete an event

Edit the Calendar title or scheduled time: `PATCH /api/v1/agent/calendar/events/{event_key}`. For example, provide `{"title":"New title","scheduled_at":"2026-10-02T16:00:00Z"}` to `edit-event`. Delete a worker-created draft with `delete-event`. Deleting the Calendar card does not cancel an already submitted execution-plane job; cancel that job through its Job Master control surface.

An event may include the remote `job_id`; it is stored with the Calendar metadata so the worker can correlate the card and execution job.
