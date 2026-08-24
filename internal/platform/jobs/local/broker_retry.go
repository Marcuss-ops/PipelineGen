package local

import (
	"context"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Progress routes through the coalescer when configured; falls back
// to direct sink passthrough if the coalescer is disabled (Window=0) or
// nil. The session guard is the same in either path — coalesce buffer
// mutation is local-only and doesn't leak worker-session validity.
//
// Why broker-level coalescing (vs worker-side on tools.go): the
// broker is the SINGLE funnelling point for in-process and remote
// workers. Worker-side coalescing would require every worker process
// (potentially 16+ per project × N projects) to maintain a separate
// buffer — extra memory + no central observability. Broker-level
// coalescing observes every worker's Progress call exactly once.
func (b *Broker) Progress(ctx context.Context, cmd appjobs.ProgressCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	if b.coalesceOn {
		return b.coalescer.Take(ctx, cmd.JobID, cmd.Progress, cmd.Message)
	}
	// Disabled coalescing: write directly to the canonical sink.
	// (Coincidentally: b.progress == b.jobs today, since *SQLiteStore
	//  satisfies both ProgressSink and job.Store. Future
	//  postgres adapter may diverge; the broker stays correct because
	//  it routes through the ProgressSink port, not the path-equal
	//  identity.)
	return b.progress.SetProgress(ctx, cmd.JobID, cmd.Progress, cmd.Message)
}

// flushPendingProgress pops the coalescer's bucket for `jobID` (if
// any) and writes it via the canonical sink BEFORE the caller
// performs a terminal transition. The order — flush first, terminal
// second — is load-bearing:
//
//	(1) The audit timeline ends with the most-recent progress
//	    row + event BEFORE the terminal row + event. A reader of
//	    job_events sees "Progress(pct=X) → JobCompleted" with no
//	    gap.
//
//	(2) SetProgress does NOT bump the canonical `revision` column
//	    today (see internal/platform/sqlite/jobs/
//	    repository_lifecycle.go:16-26). If a future PR adds revision-
//	    bumping in SetProgress, the Flush-then-Terminal ordering
//	    would FAIL because the terminal CAS would see a stale
//	    revision snapshot. That future PR MUST re-validate the
//	    ordering here or refactor Flush-then-Terminal into a single
//	    SQL tx — see comment block on retention.go for the analogous
//	    immutability invariant pattern.
//
//	(3) The coalescer's FlushJob is POP-FIRST (lock + delete +
//	    release, no SQL under lock). So if the tick loop has just
//	    popped this jobID's bucket via popBatch(), FlushJob returns
//	    (nil, nil) and we proceed directly to the terminal SQL.
//	    No double-write hazard.
//
// Errors from SetProgress during the flush are SURFACED via the
// logger but DO NOT abort the terminal transition — the canonical
// pattern is "terminal transition wins even if the last progress
// flush errors; the underlying SQL error in the terminal call is
// what the worker sees".
func (b *Broker) flushPendingProgress(ctx context.Context, jobID string) {
	if !b.coalesceOn {
		return
	}
	p, err := b.coalescer.FlushJob(jobID)
	if err != nil {
		b.log.Warn("progress coalescer FlushJob returned error (non-fatal, terminal proceeds)",
			zap.String("job_id", jobID), zap.Error(err))
		return
	}
	if p == nil {
		return // tick loop already popped it; nothing to do
	}
	if err := b.progress.SetProgress(ctx, jobID, p.pct, p.message); err != nil {
		b.log.Warn("progress coalescer terminal-flush write failed (non-fatal)",
			zap.String("job_id", jobID), zap.Int("pct", p.pct), zap.Error(err))
		return
	}
}

// Complete — order: flush-pending-progress FIRST, then terminal CAS.
// See flushPendingProgress comment block for rationale (audit timeline
// ordering + revision CAS safety invariant).
func (b *Broker) Complete(ctx context.Context, cmd appjobs.CompleteCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	b.flushPendingProgress(ctx, cmd.JobID)
	return b.jobs.Complete(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision, cmd.Result)
}

// Fail — same flush-pending-progress ordering as Complete.
func (b *Broker) Fail(ctx context.Context, cmd appjobs.FailCommand) error {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return err
	}
	b.flushPendingProgress(ctx, cmd.JobID)
	return b.jobs.Fail(ctx, cmd.JobID, cmd.WorkerID, cmd.LeaseID, cmd.ExpectedRevision, cmd.Error)
}

func (b *Broker) IsCancelled(ctx context.Context, jobID string, leaseID string) (bool, error) {
	j, err := b.jobs.Get(ctx, jobID)
	if err != nil || j == nil {
		return false, err
	}
	return j.Status == job.StatusCancelled, nil
}
