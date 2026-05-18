# Job Service

The `jobs` package provides a robust, SQLite-backed asynchronous task execution engine. It allows the system to offload long-running operations (like Artlist harvesting, YouTube extraction, or AI generation) to background workers while providing real-time status updates to the user.

## Core Features

- **Persistence**: All jobs and their events are stored in a dedicated SQLite database (`jobs.db.sqlite`), ensuring that job history and status survive system restarts.
- **Asynchronous Execution**: Jobs are processed by a pool of workers, preventing API requests from hanging.
- **Progress Tracking**: Supports granular progress updates (0-100%) and stage-based tracking (e.g., "Downloading", "Processing", "Uploading").
- **Event Logging**: A detailed audit trail for every job, allowing developers and users to see exactly what happened and when.
- **Generic Payloads**: Uses JSON serialization for job inputs and results, making it easy to add new job types without modifying the core engine.
- **Stale Job Cleanup**: Automatically identifies and marks as "failed" jobs that were interrupted by a system crash.

## Job Lifecycle

1.  **Creation**: A request comes in (e.g., `POST /api/artlist/run`). A new job record is created in the `pending` state.
2.  **Dispatch**: The `JobService` picks up the job and passes it to the appropriate domain service (e.g., `ArtlistService`).
3.  **Execution**: The domain service updates the job progress and logs events via the `JobService`.
4.  **Completion**: Upon finishing, the job is marked as `completed` or `failed`, and the final results are stored.

## Usage

```go
// Creating a job
job, err := jobsSvc.CreateJob(ctx, jobs.TypeArtlistRun, payload)

// Updating progress
jobsSvc.UpdateProgress(ctx, jobID, 50, "Downloading assets...")

// Completing a job
jobsSvc.CompleteJob(ctx, jobID, result)
```

## API Integration

The `JobsModule` provides standard endpoints for interacting with the job system:
- `GET /api/jobs`: List all recent jobs.
- `GET /api/jobs/:id`: Get detailed status and event logs for a specific job.
- `DELETE /api/jobs/:id`: Cancel or remove a job record.
