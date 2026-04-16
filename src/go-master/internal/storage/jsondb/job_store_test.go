package jsondb

import (
"context"
"os"
"path/filepath"
"testing"
"time"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
"velox/go-master/pkg/models"
)

func setupTestStore(t *testing.T) (*JobStore, string) {
t.Helper()
tmpDir, err := os.MkdirTemp("", "jobstore_test_*")
require.NoError(t, err)

store, err := NewJobStore(tmpDir)
require.NoError(t, err)
return store, tmpDir
}

func cleanupTestStore(t *testing.T, dir string) {
t.Helper()
require.NoError(t, os.RemoveAll(dir))
}

func TestNewJobStore(t *testing.T) {
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

assert.NotNil(t, store)
assert.Equal(t, tmpDir, store.dataDir)
assert.NotNil(t, store.queue)
assert.Empty(t, store.queue.Jobs)
}

func TestJobStore_SaveAndGetJob(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

job := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{"title": "Test Video"})
job.Priority = 5

require.NoError(t, store.SaveJob(ctx, job))
retrieved, err := store.GetJob(ctx, job.ID)
require.NoError(t, err)
assert.Equal(t, job.ID, retrieved.ID)
assert.Equal(t, job.Type, retrieved.Type)
assert.Equal(t, job.Priority, retrieved.Priority)
assert.Equal(t, models.StatusPending, retrieved.Status)
}

func TestJobStore_UpdateLifecycle(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

job := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
require.NoError(t, store.SaveJob(ctx, job))
require.NoError(t, store.AssignJobToWorker(ctx, job.ID, "worker-1"))

retrieved, err := store.GetJob(ctx, job.ID)
require.NoError(t, err)
assert.Equal(t, models.StatusProcessing, retrieved.Status)
assert.Equal(t, "worker-1", retrieved.WorkerID)
assert.NotNil(t, retrieved.StartedAt)

result := &models.JobResult{Success: true, VideoURL: "https://example.com/video.mp4", CompletedAt: time.Now()}
require.NoError(t, store.CompleteJob(ctx, job.ID, result))

retrieved, err = store.GetJob(ctx, job.ID)
require.NoError(t, err)
assert.Equal(t, models.StatusCompleted, retrieved.Status)
assert.Equal(t, "https://example.com/video.mp4", retrieved.Result["video_url"])
assert.NotNil(t, retrieved.CompletedAt)
}

func TestJobStore_ListJobs_FilterAndLimit(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

job1 := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
job1.Status = models.StatusPending
job1.Priority = 1
job2 := models.NewJob(models.TypeAudioProcessing, map[string]interface{}{})
job2.Status = models.StatusProcessing
job2.Priority = 2
job3 := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
job3.Status = models.StatusCompleted
job3.Priority = 3

require.NoError(t, store.SaveJob(ctx, job1))
require.NoError(t, store.SaveJob(ctx, job2))
require.NoError(t, store.SaveJob(ctx, job3))

jobs, err := store.ListJobs(ctx, models.JobFilter{Limit: 2})
require.NoError(t, err)
assert.Len(t, jobs, 2)
assert.Equal(t, job3.ID, jobs[0].ID)
assert.Equal(t, job2.ID, jobs[1].ID)

pending := models.StatusPending
jobs, err = store.ListJobs(ctx, models.JobFilter{Status: &pending})
require.NoError(t, err)
assert.Len(t, jobs, 1)
assert.Equal(t, job1.ID, jobs[0].ID)
}

func TestJobStore_GetNextPendingJob(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

job1 := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
job1.Status = models.StatusPending
job1.Priority = 1
job2 := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
job2.Status = models.StatusPending
job2.Priority = 3
job3 := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
job3.Status = models.StatusProcessing
job3.Priority = 5

require.NoError(t, store.SaveJob(ctx, job1))
require.NoError(t, store.SaveJob(ctx, job2))
require.NoError(t, store.SaveJob(ctx, job3))

next, err := store.GetNextPendingJob(ctx)
require.NoError(t, err)
assert.NotNil(t, next)
assert.Equal(t, job2.ID, next.ID)
}

func TestJobStore_UpdateJobStatusAndRetries(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupTestStore(t)
defer cleanupTestStore(t, tmpDir)

job := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{})
require.NoError(t, store.SaveJob(ctx, job))
require.NoError(t, store.UpdateJobStatus(ctx, job.ID, models.StatusFailed, "boom"))
require.NoError(t, store.IncrementJobRetries(ctx, job.ID))

retrieved, err := store.GetJob(ctx, job.ID)
require.NoError(t, err)
assert.Equal(t, models.StatusFailed, retrieved.Status)
assert.Equal(t, "boom", retrieved.Error)
assert.Equal(t, 1, retrieved.Retries)
assert.NotNil(t, retrieved.CompletedAt)
}

func TestJobStore_Persistence(t *testing.T) {
ctx := context.Background()
tmpDir, err := os.MkdirTemp("", "jobstore_persist_*")
require.NoError(t, err)
defer os.RemoveAll(tmpDir)

store1, err := NewJobStore(tmpDir)
require.NoError(t, err)

job := models.NewJob(models.TypeVideoGeneration, map[string]interface{}{"title": "Persistent Job"})
require.NoError(t, store1.SaveJob(ctx, job))

store2, err := NewJobStore(tmpDir)
require.NoError(t, err)

retrieved, err := store2.GetJob(ctx, job.ID)
require.NoError(t, err)
assert.Equal(t, job.ID, retrieved.ID)
assert.Equal(t, "Persistent Job", retrieved.Payload["title"])

_, err = os.Stat(filepath.Join(tmpDir, "queue.json"))
assert.NoError(t, err)
}
