# Artlist Service Refactoring Summary

## Overview
This document summarizes the refactoring changes made to improve reusability and align with existing codebase patterns.

## Changes Made

### 1. Extracted Interfaces (`interfaces.go`)
Created `internal/service/artlist/interfaces.go` with:
- `CandidateSearcher` - Interface for searching clip candidates
- `ClipProcessor` - Interface for processing individual clips
- `RunOrchestrator` - Interface for orchestrating run execution

### 2. Job Model Migration (`job_adapter.go`)
Created `internal/service/artlist/job_adapter.go` with:
- `JobAdapter` struct to bridge between `artlistRunRecord` and `models.Job`
- `RunRecordToJob()` - Converts run records to Job model
- `JobToRunRecord()` - Converts Job model to run records
- Support for `models.JobStatus` and retry logic via `models.Job.CanRetry()`

Key benefits:
- Reuses existing `pkg/models/job.go` types (`JobTypeStockClip`, `JobStatus`)
- Enables future retry logic with `CanRetry()` and `MaxRetries`
- Aligns with codebase's job tracking patterns

### 3. Generic Pipeline Runner (`internal/common/pipeline/runner.go`)
Created reusable pipeline in `internal/common/pipeline/`:
- `PipelineRunner` - Generic orchestrator for clip processing pipelines
- `CandidateSearcher` interface - For searching clips
- `ClipProcessor` interface - For processing clips
- `PipelineRequest/Response` - Standard request/response types
- `RunWithJob()` - Integrates with `models.Job` for tracking

Benefits:
- Other services (stock_scheduler, channel_monitor) can reuse this
- Separates concerns: search, process, persist
- Testable via interface mocks

### 4. DriveUploader Implementation (`drive_uploader.go`)
Made Artlist Service implement `interfaces.DriveUploader`:
- `UploadFile()` - Uploads file to Drive folder
- `CreateFolder()` - Creates folder on Drive
- `GetOrCreateFolder()` - Gets existing or creates folder

This aligns with `internal/core/interfaces/interfaces.go` patterns.

### 5. Pipeline Adapter (`pipeline_adapter.go`)
Created `ArtlistPipelineAdapter` to bridge Artlist Service with generic pipeline:
- Adapts Service methods to `pipeline.CandidateSearcher`
- Adapts Service methods to `pipeline.ClipProcessor`
- `NewPipelineRunner()` - Creates pipeline runner from Service
- `RunTagWithPipeline()` - Runs tag pipeline using generic runner
- `ConvertPipelineResponse()` - Converts responses

## Usage Examples

### Using the Generic Pipeline Runner
```go
// Create adapter for Artlist service
adapter := artlist.NewArtlistPipelineAdapter(service)

// Create pipeline runner
runner := pipeline.NewPipelineRunner(adapter, adapter, log)

// Run pipeline
req := &pipeline.PipelineRequest{
    Term:         "nature",
    Limit:        10,
    AutoDownload: true,
    AutoUpload:   true,
}
resp, err := runner.Run(ctx, req)
```

### Using Job Model for Run Tracking
```go
// Convert run record to Job
job := artlist.RunRecordToJob(runRecord)

// Check if can retry
if job.CanRetry() {
    // retry logic
}

// Convert back to run record
runRecord := artlist.JobToRunRecord(job)
```

## Files Modified/Created
1. `internal/service/artlist/interfaces.go` (new)
2. `internal/service/artlist/job_adapter.go` (new)
3. `internal/service/artlist/drive_uploader.go` (new)
4. `internal/service/artlist/pipeline_adapter.go` (new)
5. `internal/common/pipeline/runner.go` (new)

## Next Steps
1. Migrate existing `runs.go` to use `JobAdapter` instead of direct `artlistRunRecord`
2. Add retry logic using `models.Job.CanRetry()`
3. Refactor `stock_scheduler` or `channel_monitor` to use `internal/common/pipeline/`
4. Add unit tests for the new interfaces and adapters
