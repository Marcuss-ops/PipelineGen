package job

import "context"

// FutureJobReader is the narrow read-only lookahead port used by the
// preparation plane to inspect queued jobs WITHOUT claiming them.
//
// The contract is deliberately minimal: PeekQueued returns queued jobs in
// the same priority/creation order the real worker will claim them, but it
// MUST NOT modify any job row. In particular it never writes:
//
//	started_at  lease_id  lease_expiry  status  revision  retry_count
//
// The job officially keeps waiting: no lease is acquired, no worker is
// assigned, no state transition happens. This is what lets the preparation
// coordinator plan speculative work for jobs still in the queue without
// racing the claim path (ClaimNext / Start / RenewLease live in
// repository_claims.go and are the only mutation surface for those fields).
type FutureJobReader interface {
	PeekQueued(ctx context.Context, limit int) ([]Job, error)
}
