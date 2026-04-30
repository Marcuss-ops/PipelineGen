# Pipeline Usage Example

This document shows how to use the generic `PipelineRunner` from `internal/common/pipeline` in other services.

## Overview

The pipeline package provides a generic clip processing pipeline that can be reused across different services:

- `stock_scheduler` - for scheduled stock clip downloads
- `channel_monitor` - for monitoring and processing channels
- Any other service that needs to: search clips → process clips → track results

## Core Interfaces

### CandidateSearcher
```go
type CandidateSearcher interface {
    Search(ctx context.Context, term string, limit int) ([]ClipCandidate, error)
}
```

### ClipProcessor
```go
type ClipProcessor interface {
    ProcessClip(ctx context.Context, clipID string, opts ProcessOptions) (*ClipResult, error)
}
```

## Implementing for a New Service

### Step 1: Create an Adapter

Create an adapter that implements `CandidateSearcher` and `ClipProcessor`:

```go
package my_service

import (
    "context"
    "velox/go-master/internal/common/pipeline"
)

type MyServicePipelineAdapter struct {
    service *MyService
}

func NewMyServicePipelineAdapter(service *MyService) *MyServicePipelineAdapter {
    return &MyServicePipelineAdapter{service: service}
}

// Search implements pipeline.CandidateSearcher
func (a *MyServicePipelineAdapter) Search(ctx context.Context, term string, limit int) ([]pipeline.ClipCandidate, error) {
    // Call your service's search method
    clips, err := a.service.SearchClips(ctx, term, limit)
    if err != nil {
        return nil, err
    }

    candidates := make([]pipeline.ClipCandidate, 0, len(clips))
    for _, clip := range clips {
        candidates = append(candidates, pipeline.ClipCandidate{
            ID:       clip.ID,
            Name:     clip.Name,
            Source:   clip.Source,
            Tags:     clip.Tags,
            Duration: clip.Duration,
        })
    }
    return candidates, nil
}

// ProcessClip implements pipeline.ClipProcessor
func (a *MyServicePipelineAdapter) ProcessClip(ctx context.Context, clipID string, opts pipeline.ProcessOptions) (*pipeline.ClipResult, error) {
    // Call your service's process method
    result, err := a.service.ProcessClip(ctx, clipID, opts)
    if err != nil {
        return nil, err
    }

    return &pipeline.ClipResult{
        ClipID:    result.ClipID,
        Name:      result.Name,
        Status:    result.Status,
        DriveLink: result.DriveLink,
        FileHash:  result.FileHash,
        Error:     result.Error,
    }, nil
}
```

### Step 2: Use the Pipeline Runner

```go
func (s *MyService) RunPipeline(ctx context.Context, req *MyRequest) (*pipeline.PipelineResponse, error) {
    // Create the adapter
    adapter := NewMyServicePipelineAdapter(s)

    // Create the pipeline runner
    runner := pipeline.NewPipelineRunner(adapter, adapter, s.log)

    // Create the request
    pipelineReq := &pipeline.PipelineRequest{
        Term:         req.Term,
        Limit:        req.Limit,
        RootFolderID: req.RootFolderID,
        Strategy:     req.Strategy,
        DryRun:       req.DryRun,
        AutoDownload: true,
        AutoUpload:   true,
    }

    // Run the pipeline
    return runner.Run(ctx, pipelineReq)
}
```

### Step 3: Integrate with Job Model (Optional)

```go
func (s *MyService) RunPipelineWithJob(ctx context.Context, job *models.Job, req *MyRequest) (*pipeline.PipelineResponse, error) {
    // Create the adapter
    adapter := NewMyServicePipelineAdapter(s)

    // Create the pipeline runner
    runner := pipeline.NewPipelineRunner(adapter, adapter, s.log)

    // Create the request
    pipelineReq := &pipeline.PipelineRequest{
        Term:         req.Term,
        Limit:        req.Limit,
        RootFolderID: req.RootFolderID,
        Strategy:     req.Strategy,
        DryRun:       req.DryRun,
        AutoDownload: true,
        AutoUpload:   true,
    }

    // Run the pipeline with job tracking
    return runner.RunWithJob(ctx, job, pipelineReq)
}
```

## Benefits

1. **Reusability**: The pipeline logic is written once and can be used by any service
2. **Testability**: Easy to mock the interfaces for unit testing
3. **Consistency**: All services use the same pipeline pattern
4. **Job Integration**: Built-in support for `models.Job` tracking

## Running Tests

```bash
# Test the pipeline package
go test ./internal/common/pipeline/...

# Test a service that uses the pipeline
go test ./internal/service/my_service/...
```
