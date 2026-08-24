package local

import (
	"context"
	"errors"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func (b *Broker) ensureSession(ctx context.Context, workerID, sessionID string) error {
	// In-process workers don't register a remote session — their
	// WorkerSessionID is empty. Skip the DB probe in that case.
	// Remote workers (non-empty sessionID) MUST pass the active-session
	// check per the broker's typed sentinel contract.
	if sessionID == "" {
		return nil
	}
	if b.workers == nil {
		return nil
	}
	ok, err := b.workers.IsSessionActive(ctx, workerID, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("worker session is invalid or expired")
	}
	return nil
}

func (b *Broker) ensureJobSession(ctx context.Context, workerID, sessionID, jobID, leaseID string, expectedRevision int) error {
	if err := b.ensureSession(ctx, workerID, sessionID); err != nil {
		return err
	}
	return b.ensureLease(ctx, jobID, workerID, leaseID, expectedRevision)
}

func (b *Broker) ensureLease(ctx context.Context, jobID, workerID, leaseID string, expectedRevision int) error {
	if leaseID == "" || jobID == "" {
		return errors.New("job_id and lease_id are required")
	}
	j, err := b.jobs.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if j == nil {
		return errors.New("job not found")
	}
	if j.WorkerID != workerID || j.LeaseID != leaseID || j.Revision != expectedRevision {
		return job.ErrLeaseLost
	}
	return nil
}
