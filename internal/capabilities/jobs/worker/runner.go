// Package worker — runner.go: thin core (struct, constructor, constants)
//
// LONG-FILES-SPLIT-2026-07-06 Band A #6: the original 668-LOC
// runner.go has been decomposed into 4 single-purpose files per
// AGENTS.md Pattern 5:
//
//	runner.go         — thin core: struct, constructor, constants
//	runner_lease.go   — Run (claim loop), renewLoop, fail,
//	                    postRenewFailClosedCheck, ErrLeaseLostDuringRun
//	runner_execute.go — runLease (main job execution pipeline)
//	runner_upload.go  — uploadManifest, uploadOutputsLegacy,
//	                    OutputArtifact, sha256File,
//	                    ErrArtifactClientRequired, ErrLegacyUploadPathRemoved
//
// godlike/06 SSOT (one canonical owner per fact): each file owns
// exactly one pipeline phase.
//
// godlike/07 minimum-blast-radius: pure code-motion, zero logic changes.
package worker

import (
	"time"

	"go.uber.org/zap"

	capjobregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// DefaultLeaseTTL is the canonical lease TTL used by the runner's
// renewing loop. Cadence (DefaultRenewInterval) is half of this so
// a single renewal failure remains non-fatal — the lease still has
// TTL/2 of slack before expiry.
//
// Tuning notes:
//   - 60s is conservative for the W1 spec; long-running handlers
//     (media.artlist / extract / batch) routinely run multi-minute.
//   - The HTTP broker round-trip is sub-second under steady state;
//     TTL/2 = 30s gives ample room for a transient retry even on
//     a degraded link.
//   - Smaller TTLs are possible but a flame-detection loop at 5s
//     cadence quickly drowns the broker in Renew traffic.
const DefaultLeaseTTL = 60 * time.Second

// DefaultRenewInterval is the cadence at which the runner ticks a
// Renew call inside runLease. Equal to DefaultLeaseTTL/2 (see
// rationale on DefaultLeaseTTL).
const DefaultRenewInterval = DefaultLeaseTTL / 2

// minRenewInterval bounds the lower edge of a configurable cadence
// to prevent a misconfigured test (or production override) from
// re-entering the renewal loop faster than the broker can answer.
const minRenewInterval = 50 * time.Millisecond

type Runner struct {
	broker        jobs.Broker
	registry      *Registry
	workspace     *Workspace
	assetClient   AssetClient
	log           *zap.Logger
	workerID      string
	sessionID     string
	caps          []string
	renewInterval time.Duration // 0 → DefaultRenewInterval; clamped to >= minRenewInterval

	// observer is the kernel observability entry point (FASE 2, August
	// 2026). When non-nil, every claimed lease executed by runLease gets
	// a Run (queue_wait, wall_time, status, attempts). nil = legacy
	// un-instrumented behaviour (test fixtures keep working).
	observer  *kernobs.RunObserver
	jobLedger capjobregistry.Registry

	// claimSnapshot captures the durable prepared_at_claim_ratio KPI at the
	// INSTANT the broker's Claim() returns — before runLease executes any
	// unit, so the readiness photograph is pristine. When non-nil, every
	// successfully claimed lease snapshots its job (best-effort, never
	// blocks the claim path). nil = legacy un-instrumented behaviour.
	claimSnapshot jobs.ClaimSnapshotter
}

// NewRunner constructs a Runner with the default renewal cadence
// (DefaultRenewInterval). Production callers should not need to
// override it; the W1 Phase 7 test suite injects a faster cadence
// to exercise the renewal protocol end-to-end without a 30s wait.
func NewRunner(broker jobs.Broker, registry *Registry, workspace *Workspace, assetClient AssetClient, log *zap.Logger, workerID, sessionID string, caps []string) *Runner {
	return &Runner{
		broker:        broker,
		registry:      registry,
		workspace:     workspace,
		assetClient:   assetClient,
		log:           log,
		workerID:      workerID,
		sessionID:     sessionID,
		caps:          caps,
		renewInterval: DefaultRenewInterval,
	}
}

// WithObserver attaches the kernel observability RunObserver to the
// Runner (FASE 2, August 2026). Mirrors SetRenewInterval's fluent
// receiver pattern; nil-tolerant so test fixtures that don't wire an
// observer keep the legacy un-instrumented runLease path.
func (r *Runner) WithObserver(observer *kernobs.RunObserver) *Runner {
	r.observer = observer
	return r
}

// WithJobRegistry attaches the durable Job Registry projection to this runner.
func (r *Runner) WithJobRegistry(reg capjobregistry.Registry) *Runner { r.jobLedger = reg; return r }

// WithClaimSnapshotter attaches the claim-time KPI snapshotter to the Runner
// (prepared_at_claim_ratio photography). When set, Run captures a durable
// preparation_claim_snapshots row the moment broker.Claim returns, using the
// real attempt identity (jobID:revision). Errors are non-fatal: snapshotting
// is a control-plane side effect and must never delay or fail the claim path.
func (r *Runner) WithClaimSnapshotter(snapshotter jobs.ClaimSnapshotter) *Runner {
	r.claimSnapshot = snapshotter
	return r
}

// SetRenewInterval overrides the renewal cadence. Returns the
// receiver for chaining. Zero / negative / sub-minRenewInterval
// values are clamped to DefaultRenewInterval or minRenewInterval
// respectively so a misconfigured test cannot re-enter the renewal
// loop faster than the broker can answer (which would surface as
// broker-side TCP pressure).
func (r *Runner) SetRenewInterval(d time.Duration) *Runner {
	switch {
	case d <= 0:
		r.renewInterval = DefaultRenewInterval
	case d < minRenewInterval:
		r.renewInterval = minRenewInterval
	default:
		r.renewInterval = d
	}
	return r
}

// effectiveRenewInterval returns the cadence actually used inside
// runLease. Falls back to DefaultRenewInterval when not configured.
func (r *Runner) effectiveRenewInterval() time.Duration {
	if r.renewInterval <= 0 {
		return DefaultRenewInterval
	}
	return r.renewInterval
}
