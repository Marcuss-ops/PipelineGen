// Package job defines the canonical domain types and broker contract for
// worker-driven job execution.
package queue

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
	//
	// Returns the list of canonical AssetIDs that were created or updated
	// during finalization (derived from FinalizationResult.ArtifactRefs).
	// AZIONE 5 (July 2026): changed return from error to ([]string, error).
	CompleteWithArtifacts(ctx context.Context, cmd CompleteWithArtifactsCommand) ([]string, error)
	Fail(ctx context.Context, cmd FailCommand) error
	IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error)
}

// CompletionPort is the narrow typed port for the artifact-completion
// wire surface. Forked from Broker (Pattern 0 godlike/06 SSOT discipline:
// one canonical owner per fact, no inline port declarations scattered
// across consumers) so tests + future tooling can inject a fake
// completion port without satisfying the full 9-method Broker surface.
//
// Satisfied by:
//   - *infrastructure/jobs/local.Broker (in-process; delegates to
//     JobFinalizer.CompleteWithArtifacts via the finalization spine)
//   - *infrastructure/remote/jobbrokerclient.Client (remote; delegates
//     to POST /internal/v1/jobs/:id/complete-with-artifacts)
//
// The narrowness is load-bearing: a future "/complete-with-artifacts"
// canonical-spec drift (e.g. an optional field on the typed command)
// will fail at the *X compile-time pin (`var _ CompletionPort = (*X)(nil)`)
// across both adapters, surfacing drift as a build failure rather than
// a runtime panic.
type CompletionPort interface {
	CompleteWithArtifacts(ctx context.Context, cmd CompleteWithArtifactsCommand) ([]string, error)
}
