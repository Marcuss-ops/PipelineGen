package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/workernodes"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type Broker struct {
	jobs       job.Store
	workers    *workernodes.WorkerNodesRepository
	progress   ProgressSink
	coalescer  *ProgressCoalescer
	finalizer  finalization.JobFinalizer
	log        *zap.Logger
	coalesceOn bool // true when coalescer is configured; gated via nil-check
}

// Deps is the constructor dependency injection container (mandatory for
// PR-D setter ban, June 2026). Visible-field-line cap ≤8 (cap is enforced
// via Check 23 in scripts/ci-architectural-checks.sh — counted from a
// mirror under internal/app/lifecycle_deps_smoke_test.go).
//
// Progress + Coalescer are TYPICAL for production (coalescer buffered
// behind 100ms window). Pass Coalescer == nil to disable coalescing
// (declares Window=0 semantics inside NewProgressCoalescer) — broker
// will route Progress calls directly to the sink in that mode.
//
// Finalizer is the JobFinalizer for artifact-producing jobs (Spina
// Dorsale, Fase 3). nil = CompleteWithArtifacts will return
// ErrFinalizerNotConfigured. Non-nil = the broker delegates artifact-
// producing completions through the transactional finalization spine.
type Deps struct {
	Jobs      job.Store
	Workers   *workernodes.WorkerNodesRepository
	Progress  ProgressSink
	Coalescer *ProgressCoalescer
	Finalizer finalization.JobFinalizer
	Log       *zap.Logger
}

// New constructs the broker via Deps (PR-D setter ban, June 2026).
// Compiles a typed sentinel if any load-bearing field is missing —
// mirror of PR-Retention's ctor-validation pattern.
func New(d Deps) (*Broker, error) {
	if d.Jobs == nil {
		return nil, fmt.Errorf("local.New: Deps.Jobs is required")
	}
	if d.Progress == nil {
		return nil, fmt.Errorf("local.New: Deps.Progress is required")
	}
	if d.Log == nil {
		return nil, fmt.Errorf("local.New: Deps.Log is required")
	}
	return &Broker{
		jobs:       d.Jobs,
		workers:    d.Workers,
		progress:   d.Progress,
		coalescer:  d.Coalescer,
		finalizer:  d.Finalizer,
		log:        d.Log,
		coalesceOn: d.Coalescer != nil,
	}, nil
}

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
//	    today (see internal/infrastructure/database/sqlite/jobs/
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

// ErrFinalizerNotConfigured is returned when CompleteWithArtifacts is
// called but the broker was not wired with a JobFinalizer.
var ErrFinalizerNotConfigured = errors.New("broker: JobFinalizer not configured — CompleteWithArtifacts requires the finalization spine")

// CompleteWithArtifacts finalises a job atomically with its published
// artifacts through the JobFinalizer spine. The command's artifacts and
// events are deserialised into finalization types and passed to the
// finalizer's CompleteWithArtifacts.
//
// The lease is constructed from the broker's knowledge of the job row
// (workerID, leaseID, attempt) combined with the command's expiration
// hint.
func (b *Broker) CompleteWithArtifacts(ctx context.Context, cmd appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return nil, err
	}
	b.flushPendingProgress(ctx, cmd.JobID)

	if b.finalizer == nil {
		return nil, ErrFinalizerNotConfigured
	}

	// Deserialise artifacts from the command. The wire-format was renamed
	// PublishedArtifacts -> StagedArtifacts in P0-COMPL-5-WIRE-NAMING
	// (July 2026); the Carry value is still json.RawMessage so the unmarshal
	// shape is byte-stable across the rename. The typed StagedArtifactReference
	// surface lives on the Sender-side wire envelope at
	// internal/domain/remote/staged_artifact_reference.go (godlike/06 SSOT).
	var artifacts []finalization.PublishedArtifact
	if len(cmd.StagedArtifacts) > 0 {
		var staged remote.StagedArtifacts
		if err := json.Unmarshal(cmd.StagedArtifacts, &staged); err != nil {
			return nil, fmt.Errorf("broker: deserialise staged artifacts: %w", err)
		}
		isStaged := len(staged) > 0 && staged[0] != nil && staged[0].Destination != ""
		if isStaged {
			artifacts = make([]finalization.PublishedArtifact, 0, len(staged))
			for _, ref := range staged {
				if ref == nil {
					return nil, fmt.Errorf("broker: nil staged artifact reference")
				}
				kind, err := publishedKind(ref.Destination)
				if err != nil {
					return nil, err
				}
				requirement := finalization.ArtifactRequirementOptional
				if ref.Required {
					requirement = finalization.ArtifactRequirementRequired
				}
				artifacts = append(artifacts, finalization.PublishedArtifact{
					ArtifactID: ref.ArtifactID, Kind: kind, Filename: ref.Filename,
					MIMEType: ref.MIMEType, SizeBytes: ref.SizeBytes, SHA256: ref.SHA256,
					Requirement: requirement, IdempotencyKey: ref.ArtifactID,
					Location: finalization.AssetLocation{Provider: "local", FileID: ref.ArtifactID, DownloadLink: ref.Path, Action: finalization.PublishCreated},
				})
			}
		} else if err := json.Unmarshal(cmd.StagedArtifacts, &artifacts); err != nil {
			return nil, fmt.Errorf("broker: deserialise published artifacts: %w", err)
		}
		// Older in-process runners emitted the published envelope before
		// the typed requirement/location cutover. Normalize only that
		// legacy shape at this compatibility boundary; new staged refs
		// above always carry both values explicitly.
		for i := range artifacts {
			if !artifacts[i].Requirement.Valid() {
				artifacts[i].Requirement = finalization.ArtifactRequirementRequired
			}
			if artifacts[i].Location.Provider == "" {
				artifacts[i].Location = finalization.AssetLocation{
					Provider: "local", FileID: artifacts[i].ArtifactID,
					Action: finalization.PublishCreated,
				}
			}
		}
	}

	// Deserialise outbox events from the command.
	var events []finalization.OutboxEvent
	if len(cmd.OutboxEvents) > 0 {
		if err := json.Unmarshal(cmd.OutboxEvents, &events); err != nil {
			return nil, fmt.Errorf("broker: deserialise outbox events: %w", err)
		}
	}

	// Get the job row to compute the attempt counter and lease expiry.
	j, err := b.jobs.Get(ctx, cmd.JobID)
	if err != nil {
		return nil, fmt.Errorf("broker: get job for finalization: %w", err)
	}
	if j == nil {
		return nil, fmt.Errorf("broker: job %q not found for finalization", cmd.JobID)
	}

	// LeaseExpiry is a *time.Time in the Job struct; default to 30s
	// from now if nil (matches the Claim default).
	leaseExpiresAt := time.Now().UTC().Add(30 * time.Second)
	if j.LeaseExpiry != nil {
		leaseExpiresAt = *j.LeaseExpiry
	}

	req := finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   cmd.LeaseID,
			JobID:     cmd.JobID,
			WorkerID:  cmd.WorkerID,
			Attempt:   j.RetryCount + 1,
			ExpiresAt: leaseExpiresAt,
		},
		Result: finalization.ResultManifest{
			JobID:   cmd.JobID,
			Attempt: j.RetryCount + 1,
			Data:    cmd.ResultData,
		},
		Artifacts: artifacts,
		Events:    events,
	}

	finResult, err := b.finalizer.CompleteWithArtifacts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("broker: finalizer.CompleteWithArtifacts: %w", err)
	}

	// AZIONE 5 (July 2026): extract canonical AssetIDs from the finalizer
	// result and return them so the HTTP handler can populate the
	// CompleteArtifactsResponse.AssetIDs wire field.
	assetIDs := make([]string, 0, len(finResult.ArtifactRefs))
	for _, ref := range finResult.ArtifactRefs {
		assetIDs = append(assetIDs, ref.AssetID)
	}

	return assetIDs, nil
}

func publishedKind(destination string) (finalization.ArtifactKind, error) {
	switch destination {
	case "script":
		return finalization.KindScript, nil
	case "voiceover":
		return finalization.KindVoiceover, nil
	case "image":
		return finalization.KindImage, nil
	case "youtube_clip":
		return finalization.KindVideo, nil
	case "document", "book":
		return finalization.KindDocument, nil
	case "sound_effect":
		return finalization.KindSoundEffect, nil
	default:
		return "", fmt.Errorf("broker: unsupported staged artifact destination %q", destination)
	}
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

// WithFinalizer threads the canonical JobFinalizer into the broker
// after construction. nil is tolerated — the broker falls back to
// ErrFinalizerNotConfigured at CompleteWithArtifacts time (the typed
// sentinel surfaces the wiring gap to the operator).
//
// Returns the receiver for builder-style chaining at the composition site.
func (b *Broker) WithFinalizer(f finalization.JobFinalizer) *Broker {
	b.finalizer = f
	return b
}

// Coalescer returns the broker's progress coalescer for use by the
// lifecycle StartupStep wiring (PR-Progress / ADR-0002 §D6.4). The
// returned pointer is the same instance the broker holds in its
// Deps - tick-loop startup calls .Start(ctx) on it; tick-loop
// shutdown calls .Stop() on it.
//
// Returns nil when coalescing is disabled (Deps.Coalescer was nil at
// construction time) — the lifecycle.go startup step gates on
// non-nil before launching the ticker goroutine (matches the
// "disable coalescing" escape hatch promised by ADR §D6.4).
//
// Cheap (O(1) field read); safe to call concurrently with Take/Flush
// because the coalescer pointer is immutable post-construction (set
// in New(d Deps)).
func (b *Broker) Coalescer() *ProgressCoalescer {
	return b.coalescer
}

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
