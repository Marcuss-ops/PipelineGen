// Package job defines the canonical domain types and broker contract for
// worker-driven job execution.
package jobs

import (
	"context"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Broker is the only surface the worker should depend on.
//
// The API/server side can back it with a local in-process implementation.
// A remote worker uses the HTTP/gRPC client implementation.
//
// PR3 (June 2026): the 8 command DTOs (RegisterWorkerCommand, ClaimCommand,
// HeartbeatCommand, RenewCommand, RenewCommand, ProgressCommand,
// CompleteCommand, FailCommand, + WorkerCapabilities) are now referenced
// directly from the canonical internal/domain/job package. The intermediate
// type-alias hop in this package is gone.
type Broker interface {
	RegisterWorker(ctx context.Context, cmd job.RegisterWorkerCommand) (*WorkerSession, error)
	Heartbeat(ctx context.Context, cmd job.HeartbeatCommand) error
	Claim(ctx context.Context, cmd job.ClaimCommand) (*Lease, error)
	Renew(ctx context.Context, cmd job.RenewCommand) (*Lease, error)
	Progress(ctx context.Context, cmd job.ProgressCommand) error
	Complete(ctx context.Context, cmd job.CompleteCommand) error
	Fail(ctx context.Context, cmd job.FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}
