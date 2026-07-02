// Package job defines the canonical domain types and broker contract for
// worker-driven job execution.
package jobs

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
	// CompleteWithArtifacts finalises a job atomically with its published
	// artifacts through the JobFinalizer spine. Workers that produce
	// artifacts (videos, images, documents, voiceovers, etc.) MUST call
	// this instead of Complete so that asset records, versions, locations,
	// and outbox events are written in the same transaction as the job
	// SUCCEEDED transition.
	CompleteWithArtifacts(ctx context.Context, cmd CompleteWithArtifactsCommand) error
	Fail(ctx context.Context, cmd FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}
