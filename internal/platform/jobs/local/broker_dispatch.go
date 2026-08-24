package local

import (
	"context"
	"fmt"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

func (b *Broker) RegisterWorker(ctx context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	// W1 Phase 5: defense-in-depth on the registration side. Claim rejects
	// empty caps (see below) but a worker with empty caps that registers
	// successfully would still hold an active session and loop through
	// Claim returning ErrNoWorkerCapabilities forever. Refuse at the gate.
	if len(cmd.Capabilities.JobTypes) == 0 {
		return nil, appjobs.ErrNoWorkerCapabilities
	}
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
	if err == nil {
		// Update the in-memory heartbeat tracker so the health-check
		// RunnerProbe can verify the broker loop is alive.
		appjobs.SetBrokerAlive()
	}
	return err
}

func (b *Broker) Claim(ctx context.Context, cmd appjobs.ClaimCommand) (*appjobs.Lease, error) {
	if err := b.ensureSession(ctx, cmd.WorkerID, cmd.WorkerSessionID); err != nil {
		return nil, err
	}
	// Remote workers with empty capabilities must not claim any jobs.
	// The W1 spec (Phase 5) requires an explicit fail-closed: empty
	// capabilities means "false", not "all". Returning ErrNoWorkerCapabilities
	// makes the rejection loud at the broker layer; BuildWorkerRegistry +
	// parseAndValidateCaps already prevent this state from being entered in
	// the first place, but the broker defends in depth.
	if len(cmd.Capabilities) == 0 {
		return nil, appjobs.ErrNoWorkerCapabilities
	}
	wait := time.Duration(cmd.WaitSeconds) * time.Second
	if wait <= 0 {
		wait = 20 * time.Second
	}
	deadline := time.Now().UTC().Add(wait)
	for {
		claimed, err := b.jobs.ClaimNext(ctx, cmd.WorkerID, wait, cmd.Capabilities)
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
	// FASE 4(b) (July 2026): kernel/job.Store::RenewLease now returns
	// the typed job.RenewLeaseResult envelope (Continue |
	// CancelRequested | LeaseLost). The pre-Fase-4 `error`-only
	// return is gone. The local Broker.Renew consumes the typed
	// result to surface the post-renewal lease expiry in the
	// returned *appjobs.Lease; on LeaseStateLeaseLost / err != nil
	// the function falls through to the existing failure paths
	// (the LeaseLost typed sentinel is errors.Is-compatible with
	// sqljobs.ErrLeaseLost via the SQL adapter's `%w` wrap).
	res, err := b.jobs.RenewLease(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseTTL)
	if err != nil {
		return nil, err
	}
	// FASE 4(b) (July 2026): use the typed RenewLeaseResult.NewLeaseExpiry
	// (the canonical post-renewal expiry returned by the SQL UPDATE) as the
	// authoritative source of ExpiresAt — replacing the previous
	// time.Now().UTC().Add(cmd.LeaseTTL) which drifted by the SQL
	// roundtrip latency. The Get below is still required for the Job
	// snapshot + LeaseID (not in the typed result envelope).
	expiresAt := time.Now().UTC().Add(cmd.LeaseTTL)
	if res.NewLeaseExpiry != nil {
		expiresAt = *res.NewLeaseExpiry
	}
	j, err := b.jobs.Get(ctx, cmd.JobID)
	if err != nil || j == nil {
		return nil, err
	}
	return &appjobs.Lease{Job: j, LeaseID: j.LeaseID, ExpiresAt: expiresAt}, nil
}
