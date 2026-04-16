package jsondb

import (
"context"
"os"
"testing"
"time"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
"velox/go-master/pkg/models"
)

func setupWorkerTestStore(t *testing.T) (*WorkerStore, string) {
t.Helper()
tmpDir, err := os.MkdirTemp("", "workerstore_test_*")
require.NoError(t, err)

store, err := NewWorkerStore(tmpDir)
require.NoError(t, err)
return store, tmpDir
}

func cleanupWorkerTestStore(t *testing.T, dir string) {
t.Helper()
require.NoError(t, os.RemoveAll(dir))
}

func TestNewWorkerStore(t *testing.T) {
store, tmpDir := setupWorkerTestStore(t)
defer cleanupWorkerTestStore(t, tmpDir)

assert.NotNil(t, store)
assert.Equal(t, tmpDir, store.dataDir)
assert.NotNil(t, store.registry)
assert.Empty(t, store.registry.Workers)
}

func TestWorkerStore_SaveAndGetWorker(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupWorkerTestStore(t)
defer cleanupWorkerTestStore(t, tmpDir)

worker := models.NewWorker("worker-1", "Test Worker", "localhost", 8080, []models.WorkerCapability{models.CapVideoGeneration, models.CapFFmpeg})
require.NoError(t, store.SaveWorker(ctx, worker))

retrieved, err := store.GetWorker(ctx, worker.ID)
require.NoError(t, err)
assert.Equal(t, worker.ID, retrieved.ID)
assert.Equal(t, worker.Name, retrieved.Name)
assert.Equal(t, worker.Host, retrieved.Host)
assert.Equal(t, worker.Port, retrieved.Port)
assert.Len(t, retrieved.Capabilities, 2)
assert.True(t, retrieved.HasCapability(models.CapVideoGeneration))
}

func TestWorkerStore_LoadAndSaveWorkers(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupWorkerTestStore(t)
defer cleanupWorkerTestStore(t, tmpDir)

workers := map[string]*models.Worker{
"worker-1": models.NewWorker("worker-1", "Worker 1", "host1", 8081, nil),
"worker-2": models.NewWorker("worker-2", "Worker 2", "host2", 8082, nil),
}

require.NoError(t, store.SaveWorkers(ctx, workers))
loaded, err := store.LoadWorkers(ctx)
require.NoError(t, err)
assert.Len(t, loaded, 2)
assert.Equal(t, "Worker 1", loaded["worker-1"].Name)
assert.Equal(t, "Worker 2", loaded["worker-2"].Name)
}

func TestWorkerStore_GetActiveWorkersAndByCapability(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupWorkerTestStore(t)
defer cleanupWorkerTestStore(t, tmpDir)

worker1 := models.NewWorker("worker-1", "Video Worker", "localhost", 8080, []models.WorkerCapability{models.CapVideoGeneration})
worker1.LastSeen = time.Now()
worker1.Stats.JobsCompleted = 10

worker2 := models.NewWorker("worker-2", "Old Worker", "localhost", 8081, []models.WorkerCapability{models.CapAudioProcessing})
worker2.LastSeen = time.Now().Add(-2 * time.Minute)

worker3 := models.NewWorker("worker-3", "Video Worker 2", "localhost", 8082, []models.WorkerCapability{models.CapVideoGeneration, models.CapRemotion})
worker3.LastSeen = time.Now().Add(-10 * time.Second)
worker3.Stats.JobsCompleted = 20

require.NoError(t, store.SaveWorker(ctx, worker1))
require.NoError(t, store.SaveWorker(ctx, worker2))
require.NoError(t, store.SaveWorker(ctx, worker3))

active, err := store.GetActiveWorkers(ctx, 30*time.Second)
require.NoError(t, err)
assert.Len(t, active, 2)
assert.Contains(t, active, "worker-1")
assert.Contains(t, active, "worker-3")

videoWorkers, err := store.GetWorkersByCapability(ctx, models.CapVideoGeneration)
require.NoError(t, err)
assert.Len(t, videoWorkers, 2)
assert.Equal(t, "worker-3", videoWorkers[0].ID)
assert.Equal(t, "worker-1", videoWorkers[1].ID)
}

func TestWorkerStore_UpdateWorkerState(t *testing.T) {
ctx := context.Background()
store, tmpDir := setupWorkerTestStore(t)
defer cleanupWorkerTestStore(t, tmpDir)

worker := models.NewWorker("worker-1", "Test Worker", "localhost", 8080, nil)
require.NoError(t, store.SaveWorker(ctx, worker))
require.NoError(t, store.UpdateWorkerStatus(ctx, worker.ID, models.WorkerBusy))
require.NoError(t, store.UpdateWorkerJob(ctx, worker.ID, "job-123"))
require.NoError(t, store.TouchWorker(ctx, worker.ID))

retrieved, err := store.GetWorker(ctx, worker.ID)
require.NoError(t, err)
assert.Equal(t, models.WorkerBusy, retrieved.Status)
assert.Equal(t, "job-123", retrieved.CurrentJobID)
assert.NotNil(t, retrieved.Stats.LastJobStarted)

require.NoError(t, store.UpdateWorkerJob(ctx, worker.ID, ""))
retrieved, err = store.GetWorker(ctx, worker.ID)
require.NoError(t, err)
assert.Equal(t, models.WorkerIdle, retrieved.Status)
assert.Empty(t, retrieved.CurrentJobID)
}

func TestWorkerStore_GetAvailableWorkersAndPersistence(t *testing.T) {
ctx := context.Background()
tmpDir, err := os.MkdirTemp("", "workerstore_persist_*")
require.NoError(t, err)
defer os.RemoveAll(tmpDir)

store, err := NewWorkerStore(tmpDir)
require.NoError(t, err)

worker1 := models.NewWorker("worker-1", "Available", "localhost", 8080, nil)
worker1.Status = models.WorkerIdle
worker1.Stats.JobsCompleted = 10
worker2 := models.NewWorker("worker-2", "Busy", "localhost", 8081, nil)
worker2.Status = models.WorkerBusy
worker2.CurrentJobID = "job-1"

require.NoError(t, store.SaveWorker(ctx, worker1))
require.NoError(t, store.SaveWorker(ctx, worker2))

available, err := store.GetAvailableWorkers(ctx)
require.NoError(t, err)
assert.Len(t, available, 1)
assert.Equal(t, "worker-1", available[0].ID)

store2, err := NewWorkerStore(tmpDir)
require.NoError(t, err)
retrieved, err := store2.GetWorker(ctx, worker1.ID)
require.NoError(t, err)
assert.Equal(t, worker1.ID, retrieved.ID)
assert.Equal(t, "Available", retrieved.Name)
}
