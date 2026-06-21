package local

import (
	"context"
	"errors"
	"fmt"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
)

type Broker struct {
	jobs    domainjob.Store
	workers *assets.WorkerNodesRepository
}

func New(jobs domainjob.Store, workers *assets.WorkerNodesRepository) *Broker {
	return &Broker{jobs: jobs, workers: workers}
}

func (b *Broker) RegisterWorker(ctx context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	if b.workers == nil {
		return nil, fmt.Errorf("worker repository not configured")
	}
	return b.workers.Register(ctx, cmd)
}

func (b *Broker) Heartbeat(ctx context.Context, cmd appjobs.HeartbeatCommand) error {
	if b.workers == nil {
		return nil
	}
	_, err := b.workers.Heartbeat(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.SessionTTL)
	return err
}

func (b *Broker) Claim(ctx context.Context, cmd appjobs.ClaimCommand) (*appjobs.Lease, error) {
	if err := b.ensureSession(ctx, cmd.WorkerID, cmd.WorkerSessionID); err != nil {
		return nil, err
	}
	wait := time.Duration(cmd.WaitSeconds) * time.Second
	if wait <= 0 {
		wait = 20 * time.Second
	}
	deadline := time.Now().UTC().Add(wait)
	for {
		types := cmd.Capabilities
		if len(types) == 0 {
			types = nil
		}
		claimed, err := b.jobs.ClaimNext(ctx, cmd.WorkerID, wait, types)
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			return &appjobs.Lease{Job: claimed, LeaseID: claimed.LeaseID, ExpiresAt: time.Now().UTC().Add(wait)}, nil
		}
		if time.Now().UTC().After(deadline) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (b *Broker) Renew(ctx context.Context, cmd appjobs.RenewCommand) (*appjobs.Lease, error) {
	if err := b.ensureSession(ctx, cmd.WorkerID, cmd.WorkerSessionID); err != nil {
		return nil, err
	}
	if err := b.ensureLease(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return nil, err
	}
	if err := b.jobs.RenewLease(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseTTL); err != nil {
		return nil, err
	}
	j, err := b.jobs.Get(ctx, cmd.JobID)
	if err != nil || j == nil {
		return nil, err
	}
	return &appjobs.Lease{Job: j, LeaseID: j.LeaseID, ExpiresAt: time.Now().UTC().Add(cmd.LeaseTTL)}, nil
}

func (b *Broker) Progress(ctx context.Context, cmd appjobs.ProgressCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	return b.jobs.SetProgress(ctx, cmd.JobID, cmd.Progress, cmd.Message)
}

func (b *Broker) Complete(ctx context.Context, cmd appjobs.CompleteCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	return b.jobs.Complete(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision, cmd.Result)
}

func (b *Broker) Fail(ctx context.Context, cmd appjobs.FailCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	return b.jobs.Fail(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision, cmd.Error)
}

func (b *Broker) IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error) {
	j, err := b.jobs.Get(ctx, jobID)
	if err != nil || j == nil {
		return false, err
	}
	return j.Status == domainjob.StatusCancelled, nil
}

func (b *Broker) ensureSession(ctx context.Context, workerID, sessionID string) error {
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
		return sqljobs.ErrLeaseLost
	}
	return nil
}
