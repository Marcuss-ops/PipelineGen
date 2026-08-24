package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// ExpectedRevision returns the worker's current view of the canonical
// job revision. It tracks the lease revision as observed at Claim and
// monotonically advanced by Renew. Use this — not the snapshot in
// jobs.Lease.Job — when constructing subsequent Complete/Fail/
// Progress/Renew commands after a renewal.
//
// Why: SQLiteStore.RenewLease advances revision on every successful
// renewal (see internal/platform/sqlite/jobs/repository_claims.go::RenewLease).
// Phase 7 introduced a renewing runLease loop, so a snapshot revision
// captured at Claim is stale after the first renewal. Using it would
// cause broker.Complete to return ErrLeaseLost (revision mismatch)
// and silently downgrade a successful job to Failed. Hard-won lesson
// from Phase 6→7: never let Tools expose a stale revision to the
// runner by accident.
//
// Concurrency: t.revision is an atomic.Int64 (NOT a plain int). It's
// written by Tools.Renew inside the renewLoop goroutine and read by
// Tools.Complete / Fail / Progress in the main runLease goroutine.
// Under -race this is mandatory; a plain int triggers TEST
// FAILURE. Use int(t.revision.Load()) to read; use
// t.revision.Store(int64(j.Revision)) to write.
func (t *Tools) ExpectedRevision() int {
	return int(t.revision.Load())
}

// Complete forwards a successful job outcome to the broker using
// the worker's current ExpectedRevision (post-renewal). Mirrors the
// existing Progress/IsCancelled/Renew convention of Tools bridging
// between the runner and the broker.
func (t *Tools) Complete(ctx context.Context, result json.RawMessage) error {
	return t.broker.Complete(ctx, jobs.CompleteCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: int(t.revision.Load()),
		Result:           result,
	})
}

// CompleteWithArtifacts forwards a successful artifact-producing job
// outcome to the broker using the JobFinalizer spine. The artifacts
// and events are JSON-serialised on the worker side and deserialised
// by the broker before the finalization transaction.
//
// Workers that produce artifacts (videos, images, documents,
// voiceovers, etc.) MUST call this instead of Complete.
//
// AZIONE 5 (July 2026): broker now returns canonical AssetIDs from
// finalization. The runner does not need these IDs (only the HTTP
// handler does), so Tools discards them and returns only the error.
func (t *Tools) CompleteWithArtifacts(ctx context.Context, resultData json.RawMessage, publishedArtifacts json.RawMessage, outboxEvents json.RawMessage) ([]string, error) {
	return t.broker.CompleteWithArtifacts(ctx, jobs.CompleteWithArtifactsCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: int(t.revision.Load()),
		ResultData:       resultData,
		StagedArtifacts:  publishedArtifacts,
		OutboxEvents:     outboxEvents,
	})
}

// Fail forwards a terminal job outcome (with a stringified error)
// to the broker using the worker's current ExpectedRevision
// (post-renewal). Used by the runner when the handler returned an
// err, by Assets tools when download fails, and by the renewal loop
// when Renew returned ErrLeaseLost mid-execution.
func (t *Tools) Fail(ctx context.Context, errStr string) error {
	return t.broker.Fail(ctx, jobs.FailCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: int(t.revision.Load()),
		Error:            errStr,
	})
}

type AssetClient interface {
	Download(ctx context.Context, assetID string) (io.ReadCloser, string, error)
	UploadFile(ctx context.Context, assetID, filePath string) error
}

// Tools is the broker-facing facade for a single in-flight job.
// All fields beyond broker/workspace/assets are immutable post-
// construction except revision, which is atomically advanced by
// Renew and atomically observed by every subsequent broker call.
//
// Field write/read rules:
//   - broker, workerID, sessionID, jobID, leaseID, workspace,
//     assetClient: written ONCE in NewTools, never mutated, so
//     concurrent reads are safe without sync.
//   - revision: written by Renew (renewLoop goroutine) and read
//     by Complete/Fail/Progress/ExpectedRevision (runLease main
//     goroutine and external callers). atomic.Int64 is REQUIRED
//     here. -race tests will fail loudly if regressed to plain int.
//
// eventStore is the narrow port needed to persist timeline events.
// The concrete broker (local or remote) may or may not implement it;
// when it does not, event emission becomes a no-op.
type eventStore interface {
	AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error
}

type Tools struct {
	broker      jobs.Broker
	store       eventStore
	workerID    string
	sessionID   string
	jobID       string
	leaseID     string
	revision    atomic.Int64
	workspace   string
	assetClient AssetClient
	ledger      *jobs.JobRegistryRecorder
}

// NewTools constructs a Tools. Note: revision is initialised AFTER
// the literal because atomic.Int64 has no constructor pattern
// compatible with struct literals in Go pre-1.19; the Load/Store
// pattern is preferred. The single Store here is the only publish
// of the initial revision; subsequent Stores come from Renew.
func NewTools(broker jobs.Broker, store eventStore, workerID, sessionID string, j *job.Job, workspace string, assetClient AssetClient) *Tools {
	t := &Tools{
		broker:      broker,
		store:       store,
		workerID:    workerID,
		sessionID:   sessionID,
		jobID:       j.ID,
		leaseID:     j.LeaseID,
		workspace:   workspace,
		assetClient: assetClient,
	}
	t.revision.Store(int64(j.Revision))
	return t
}

func (t *Tools) WithJobRegistry(ledger *jobs.JobRegistryRecorder) *Tools {
	t.ledger = ledger
	return t
}

func (t *Tools) Progress(ctx context.Context, progress int, message string) error {
	err := t.broker.Progress(ctx, jobs.ProgressCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: int(t.revision.Load()),
		Progress:         progress,
		Message:          message,
	})
	if t.ledger != nil {
		t.ledger.RecordProgress(ctx, t.jobID, progress, message)
	}
	return err
}

// Event records a typed timeline event for the in-flight job.
// It is a thin wrapper around the canonical job.Store.AddEvent port
// so handlers can emit observability events without touching SQLite
// directly. Errors are logged but not propagated — event emission
// must never fail the job.
func (t *Tools) Event(ctx context.Context, eventType, message string, data map[string]any) error {
	var err error
	if t.store != nil {
		err = t.store.AddEvent(ctx, t.jobID, eventType, message, data)
	}
	if t.ledger != nil {
		t.ledger.RecordEvent(ctx, t.jobID, eventType, message, data)
	}
	return err
}

func (t *Tools) IsCancelled(ctx context.Context) (bool, error) {
	return t.broker.IsCancelled(ctx, t.jobID, t.leaseID)
}

func (t *Tools) Renew(ctx context.Context, leaseTTL time.Duration) error {
	lease, err := t.broker.Renew(ctx, jobs.RenewCommand{
		WorkerID:         t.workerID,
		WorkerSessionID:  t.sessionID,
		JobID:            t.jobID,
		LeaseID:          t.leaseID,
		ExpectedRevision: int(t.revision.Load()),
		LeaseTTL:         leaseTTL,
	})
	if err != nil {
		return err
	}
	if lease != nil && lease.Job != nil {
		t.revision.Store(int64(lease.Job.Revision))
	}
	return nil
}

func (t *Tools) DownloadAsset(ctx context.Context, assetID string) (string, error) {
	if t.assetClient == nil {
		return "", fmt.Errorf("asset client not configured")
	}
	rc, filename, err := t.assetClient.Download(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	if filename == "" {
		filename = assetID
	}
	dst := filepath.Join(t.workspace, "input", filename)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", err
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return "", err
	}
	return dst, nil
}

func ParseInputAssets(payload json.RawMessage) []string {
	var raw struct {
		InputAssets []struct {
			AssetID string `json:"asset_id"`
		} `json:"input_assets"`
	}
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	out := make([]string, 0, len(raw.InputAssets))
	for _, a := range raw.InputAssets {
		if a.AssetID != "" {
			out = append(out, a.AssetID)
		}
	}
	return out
}
