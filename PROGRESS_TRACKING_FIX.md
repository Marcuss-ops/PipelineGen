# Async Pipeline Progress Tracking Fix

## Problem
The async pipeline progress tracking was stuck showing `script_generation: 5%` throughout the entire execution, even during clip download/upload operations that took 10-30 minutes.

### Root Cause
The `AsyncPipelineService.executePipeline()` method:
1. Set `Progress = 5` at the start
2. Called `GenerateScriptWithClips()` — a **blocking call** with zero progress updates
3. Only updated `Progress = 100` when the entire pipeline completed

The code had a comment: *"Dobbiamo wrappare il servizio originale per aggiornare il progresso"* (We need to wrap the original service to update progress) — but this was never implemented.

## Solution
Implemented a **progress callback system** that reports progress at each pipeline step.

### Changes Made

#### 1. Added Progress Callback Interface
**File:** `src/go-master/internal/service/scriptclips/service.go`

```go
// ProgressCallback is called during pipeline execution to report progress
type ProgressCallback func(step string, progress int, message string, entityName string, clipsDone int, clipsTotal int)

type ScriptClipsRequest struct {
    // ... existing fields ...
    ProgressCallback ProgressCallback `json:"-"` // Not serialized, used for progress updates
}
```

#### 2. Updated GenerateScriptWithClips to Report Progress
The method now calls `reportProgress()` at each step:

| Step | Progress Range | What Happens |
|------|---------------|--------------|
| `script_generation` | 0% → 15% | Ollama generates the script |
| `entity_extraction` | 15% → 30% | Entities extracted from script segments |
| `timestamp_calculation` | ~35% | Timestamps calculated for each segment |
| `clip_processing` | 35% | Start of clip processing |
| `clip_download` | 35% → 95% | **Per-entity progress** as clips are found/downloaded/uploaded |
| `completed` | 100% | Pipeline finished |

**Key improvement:** During clip processing (the longest phase), progress updates after **each entity**:
```go
progressPct := 35 + int(float64(currentDone)/float64(totalEntities)*60)
reportProgress("clip_download", progressPct,
    fmt.Sprintf("✓ Clip found for '%s' (%d/%d)", task.entityName, currentDone, totalEntities),
    task.entityName, currentDone, totalEntities)
```

#### 3. Wired Progress Callback in AsyncPipelineService
**File:** `src/go-master/internal/service/asyncpipeline/service.go`

The `executePipeline()` method now creates a callback that updates the `PipelineJob`:

```go
progressCallback := func(step string, progress int, message string, entityName string, clipsDone int, clipsTotal int) {
    s.updateJob(jobID, func(j *PipelineJob) {
        j.CurrentStep = step
        j.Progress = progress
        if entityName != "" {
            j.CurrentEntity = entityName
        }
        if clipsTotal > 0 {
            j.TotalClips = clipsTotal
            j.ClipsFound = clipsDone
            j.ClipsMissing = clipsTotal - clipsDone
        }
    })
    
    logger.Info("Pipeline progress update",
        zap.String("job_id", jobID),
        zap.String("step", step),
        zap.Int("progress", progress),
        zap.String("message", message),
    )
}

req.ProgressCallback = progressCallback
```

### Progress Updates Flow

```
Client polls: GET /api/pipeline/status/{job_id}
                ↓
AsyncPipelineHandler.GetJobStatus()
                ↓
AsyncPipelineService.GetJobStatus()
                ↓
Returns PipelineJob with:
  - Progress: 67 (updated by callback)
  - CurrentStep: "clip_download"
  - CurrentEntity: "kickboxing"
  - ClipsFound: 8
  - TotalClips: 12
```

### Example Progress Timeline

For a pipeline processing 12 entities:

| Time | Step | Progress | Details |
|------|------|----------|---------|
| 0s | `script_generation` | 5% | "Generating script via Ollama..." |
| 45s | `script_generation` | 15% | "Script generated (247 words)" |
| 46s | `entity_extraction` | 20% | "Analyzing script and extracting entities..." |
| 62s | `entity_extraction` | 30% | "Entity extraction completed (5 segments, 12 entities)" |
| 63s | `clip_processing` | 35% | "Starting clip processing for 12 entities" |
| 75s | `clip_download` | 40% | "✓ Clip found for 'Andrew Tate' (1/12)" |
| 90s | `clip_download` | 45% | "✓ Clip found for 'kickboxing' (2/12)" |
| 105s | `clip_download` | 50% | "✗ Clip not found for 'Romania' (3/12)" |
| ... | ... | ... | ... |
| 280s | `clip_download` | 95% | "✓ Clip found for 'TikTok' (12/12)" |
| 285s | `completed` | 100% | "Pipeline completed: 10 clips found, 2 missing" |

## Testing

### Automated Test
Run the test script:
```bash
./test_pipeline_progress.sh
```

This will:
1. Start a pipeline job via the API
2. Poll `/api/pipeline/status/{job_id}` every 3 seconds
3. Display real-time progress updates
4. Verify progress changes from 0% → 100%

### Manual Test
```bash
# Start a job
curl -X POST http://localhost:8080/api/pipeline/start \
  -H "Content-Type: application/json" \
  -d '{
    "source_text": "Test text about a topic...",
    "title": "Test Topic",
    "duration": 60
  }'

# Poll status (replace JOB_ID)
watch -n 3 'curl -s http://localhost:8080/api/pipeline/status/JOB_ID | python3 -m json.tool'
```

### Expected Output
You should see progress updating during clip processing:
```json
{
  "ok": true,
  "job": {
    "id": "pipeline_1234567890",
    "status": "running",
    "progress": 67,
    "current_step": "clip_download",
    "current_entity": "kickboxing",
    "total_clips": 12,
    "clips_found": 8,
    "clips_missing": 4
  }
}
```

## Impact

### Before Fix
- Progress stuck at 5% for 10-30 minutes
- No visibility into what the pipeline was doing
- Users couldn't tell if it was working or stuck

### After Fix
- Progress updates every few seconds during clip processing
- Shows which entity is being processed
- Shows clip success/failure counts in real-time
- Clear visibility into pipeline progress

## Files Modified

1. `src/go-master/internal/service/scriptclips/service.go`
   - Added `ProgressCallback` type
   - Added `ProgressCallback` field to `ScriptClipsRequest`
   - Added `reportProgress()` helper function
   - Added progress calls at each pipeline step
   - Added per-entity progress during clip download

2. `src/go-master/internal/service/asyncpipeline/service.go`
   - Created progress callback function in `executePipeline()`
   - Wired callback to `req.ProgressCallback`
   - Callback updates `PipelineJob` fields (Progress, CurrentStep, CurrentEntity, Clips*)
   - Logs each progress update for debugging

3. `test_pipeline_progress.sh` (new)
   - Automated test script for progress tracking verification
