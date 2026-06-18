// Package job defines the canonical domain types and broker contract for
// worker-driven job execution.
package job

import "context"

// Broker is the only surface the worker should depend on.
//
// The API/server side can back it with a local in-process implementation.
// A remote worker uses the HTTP/gRPC client implementation.
type Broker interface {
	RegisterWorker(ctx context.Context, cmd RegisterWorkerCommand) (*WorkerSession, error)
	Heartbeat(ctx context.Context, cmd HeartbeatCommand) error
	Claim(ctx context.Context, cmd ClaimCommand) (*Lease, error)
	Renew(ctx context.Context, cmd RenewCommand) (*Lease, error)
	Progress(ctx context.Context, cmd ProgressCommand) error
	Complete(ctx context.Context, cmd CompleteCommand) error
	Fail(ctx context.Context, cmd FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}
