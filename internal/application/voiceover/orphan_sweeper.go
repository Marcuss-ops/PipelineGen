// Package voiceover — orphan_sweeper.go (audit P0 #4 commit B/2, July 2026).
//
// OrphanSweeper is the background goroutine that compensates for
// partial-failures in upload_intents. The A/2 use case
// (upload_intent.go) keeps rows at 'uploaded' on Step 4 / 4.5
// failure so this sweeper can detect + recover Drive-side orphans
// without breaking the canonical state machine.
//
// COMPENSATION SEMANTICS (per stale-row status, godlike/07 typed):
//
//	'uploaded' stale (older than uploadedTTL):
//	  The intent row stuck at 'uploaded' (Drive file exists locally
//	  AND on Drive, but the chain didn't progress to 'finalized' /
//	  'completed'). The sweeper:
//	    1. Calls DriveDeleter.Trash on the stamp'd drive_file_id
//	       (moves to Drive trash; NOT permanent delete — operator
//	       can recover within the 30-day Drive trash retention).
//	    2. Calls Repo.MarkFailed with reason
//	       "orphan_sweep: uploaded_no_finalize".
//	  The intent row transitions to 'failed' so future sweeps
//	  skip it. The Reconciled{uploaded_cleanup} metric is bumped.
//
//	'pending' stale (older than pendingTTL):
//	  No Drive file exists (Step 2 hadn't completed). The sweeper
//	  ONLY emits Repo.MarkFailed with reason
//	  "orphan_sweep: pending_timeout". NO Drive action. The
//	  Reconciled{pending_timeout} metric is bumped.
//
// TTL PARTITION STRATEGY: Repo.ListPending returns BOTH 'pending'
// + 'uploaded' rows in a single call (single WHERE filter, indexed
// by migration 116 idx_upload_intents_status_updated_at). The
// sweeper issues ONE ListPending with `now - min(pendingTTL,
// uploadedTTL)` (so neither stale pool is missed) and PARTITIONS
// by status at the application layer, applying the per-state TTL
// check before compensating. This is what the canonical SQL index
// expects; issuing two ListPending scans would double the index
// work for no benefit.
//
// LIFECYCLE: registered in internal/app/lifecycle.go::startBackgroundJobs
// under the runMaintenance block (parallel to deletion-reconciler).
// Goroutine exits on ctx.Done() — canonical pattern; no explicit
// Stop method on the OrphanSweeper type itself. The StartupStep
// exposes a `Stop: func(_ context.Context) error { return nil }`
// (no-op) for the lifecycle's teardown symmetry.
//
// ERROR ISOLATION (per-row): per the thinker's verdict on tick
// behaviour, ListPending failure skips the tick (logs warn, rolls
// forward to next tick — does NOT abort the loop). Per-row
// failures (Drive.Trash / MarkFailed errors) log warn and
// continue with the next row. This prevents one bad row from
// aborting the entire sweep.
//
// METRIC SEMANTICS: Runs.Inc() ONCE per Run invocation (per
// process boot, NOT per sweep tick). Reconciled.WithLabelValues
// .Inc() per row COMPENSATED (skipped rows + idempotent-NOT-FOUND
// rows do NOT count — godlike/07 NO_FAKE_AVAILABILITY: a metric
// increment implies the canonical recovery, not a "tried but
// failed" record).
package voiceover

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// ─── Port surface (Pattern 0, AGENTS.md / godlike/06) ──────────────────────

// OrphanDriveDeleter is the narrow application-layer port for the
// orphan sweeper's Drive-side compensation (only Trash — the
// sweeper NEVER permanently deletes an orphan because operators
// might want to recover conservatively-trashed files within Drive's
// 30-day trash retention).
//
// The pre-existing outbox.DriveDeleter port (declared in
// internal/application/jobs/outbox/ports.go:126) satisfies this
// interface via Go's structural conformance:
// *drive.FileLifecycleAdapter has both Trash + Delete methods,
// satisfying this narrower's single method.
//
// Pattern 0 (consumer declares the interface) keeps the orphan
// sweeper decoupled from google.golang.org/api/drive/v3 (the
// Drive port is consumed, never imported). Compile-time
// assertions lock conformance in orphan_sweeper_test.go
// (mock-side) and at production wiring sites (lifecycle.go's
// StartupStep closure — the assertion happens implicitly via
// Go's structural typing when the lifecycle.go closure
// constructs a *drive.FileLifecycleAdapter and assigns it).
type OrphanDriveDeleter interface {
	Trash(ctx context.Context, fileID string) error
}

// ─── Metrics ────────────────────────────────────────────────────────────────

// Metrics is the typed injection bundle for the orphan sweeper's
// Prometheus counters. Production wiring (lifecycle.go) passes
// the package-level observability.OrphanSweeperRunsTotal +
// observability.OrphanSweeperReconciledTotal; tests construct
// local mocks with prometheus.NewCounter / NewCounterVec (NOT
// promauto — auto-registers globally and pollutes the default
// Prometheus registry on every test run).
//
// The struct shape locks the field names at compile-time, so a
// future drift here surfaces as a vet error (drift surface:
// func-typed metrics would silently-mismatch via closure
// captures; struct fields lock the contract at compile time).
type Metrics struct {
	// Runs is incremented ONCE per OrphanSweeper.Run invocation
	// (per process boot, NOT per sweep tick). Per user-spec
	// "ad ogni start" of the goroutine.
	Runs prometheus.Counter
	// Reconciled is a CounterVec with label "outcome" — possible
	// values: OutcomeUploadedCleanup (Drive.Trash + MarkFailed on
	// stale 'uploaded'), OutcomePendingTimeout (MarkFailed only,
	// no Drive action on stale 'pending').
	Reconciled *prometheus.CounterVec
}

// Outcome labels for the Reconciled counter.
const (
	// OutcomeUploadedCleanup signals a stale 'uploaded' row was
	// recovered. A "Trash absent" outcome (intent row compensated
	// but Drive.Trash errored) still emits this label — the
	// operator visibility is on intent-row-level recovery, not
	// the Drive-side trash success (godlike/07 NO_FAKE_AVAILABILITY:
	// the sweeper must NOT lie about Drive state; the broken
	// Trash is reported via the TrashErrors SweepStats field
	// and the per-row warn-level log).
	OutcomeUploadedCleanup = "uploaded_cleanup"
	// OutcomePendingTimeout signals a stale 'pending' row was
	// timed out (MarkFailed ONLY; no Drive file existed so no
	// Drive-side action needed).
	OutcomePendingTimeout = "pending_timeout"
)

// ─── Sweeper ────────────────────────────────────────────────────────────────

// OrphanSweeperDeps is the constructor dep bundle (godlike/05
// wiring-error rule: mandatory deps panic on nil).
type OrphanSweeperDeps struct {
	Repo         UploadIntentsRepository // mandatory
	DriveDeleter OrphanDriveDeleter      // mandatory
	Logger       *zap.Logger             // nil-safe via zap.NewNop()
	Metrics      *Metrics                // mandatory
	Tick         time.Duration           // mandatory (>0)
	PendingTTL   time.Duration           // mandatory (>0)
	UploadedTTL  time.Duration           // mandatory (>0)
}

// OrphanSweeper is the background ticker-loop service that
// compensates for partial-failures in upload_intents (godlike/07
// NO_FAKE_AVAILABILITY closure): rows stuck at pre-completion
// states get Drive.Trash + MarkFailed so they don't dangle.
type OrphanSweeper struct {
	deps OrphanSweeperDeps
}

// NewOrphanSweeper constructs the sweeper. Tick / TTLs are
// constructor fields (NOT runtime-configurable) so wiring decisions
// land during composition (per AGENTS.md godlike/05 wiring-error
// rule). A future config-driven version would take a *config.Config
// + parse durations from string — out of scope for B/2.
func NewOrphanSweeper(deps OrphanSweeperDeps) *OrphanSweeper {
	if deps.Repo == nil {
		panic("voiceover.NewOrphanSweeper: Repo is required (godlike/05 wiring-error fail-fast)")
	}
	if deps.DriveDeleter == nil {
		panic("voiceover.NewOrphanSweeper: DriveDeleter is required")
	}
	if deps.Metrics == nil {
		panic("voiceover.NewOrphanSweeper: Metrics is required")
	}
	if deps.Tick <= 0 {
		panic("voiceover.NewOrphanSweeper: Tick must be > 0")
	}
	if deps.PendingTTL <= 0 {
		panic("voiceover.NewOrphanSweeper: PendingTTL must be > 0")
	}
	if deps.UploadedTTL <= 0 {
		panic("voiceover.NewOrphanSweeper: UploadedTTL must be > 0")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &OrphanSweeper{deps: deps}
}

// SweepStats is the per-tick return value from sweep(). Operators
// can read these per-tick stats from the log line emitted by Run
// after each tick to spot trends:
//
//   - high trash_errors per minute → Drive-side flakiness
//     (likely a transient Drive API outage; ops should check
//     Drive-Side health dashboards).
//   - high mark_failed_errs per minute → DB lock contention
//     (likely a busy voiceover-pipeline-write window; ops should
//     check the SQLite WAL/busy_timeout config).
type SweepStats struct {
	PendingStale   int // rows in 'pending' status returned by ListPending (pre-TTL filter)
	UploadedStale  int // rows in 'uploaded' status returned by ListPending (pre-TTL filter)
	PendingDone    int // MarkFailed succeeded for pending rows
	UploadedDone   int // MarkFailed succeeded for uploaded rows
	TrashErrors    int // Drive.Trash returned an error (intent row still compensated)
	MarkFailedErrs int // MarkFailed returned non-NotFound, non-nil error
}

// compensateOutcome is the per-row outcome enum — keeps the
// switch in sweep() explicit and avoids silently dropping the
// trash-error-vs-reconcile-error distinction.
type compensateOutcome int

const (
	outcomeError compensateOutcome = iota
	outcomeReconciled
	// outcomeIdempotentSkip: row already at 'failed' (race with
	// another sweeper / manual intervention). No metric bump —
	// already accounted for by the previous transition.
	outcomeIdempotentSkip
)

// Run launches the ticker-loop. One tick at Tick interval. Each
// tick: sweep() partitions stale rows by status + applies
// per-state TTL + compensates per row. Exit on ctx.Done().
//
// godlike/07 contract: per-row failures are isolated so one bad
// row doesn't abort an entire sweep. ListPending FAILURE skips
// the tick entirely (logs warn, rolls to next tick — does NOT
// abort the loop). The Reconciled metric is only incremented on
// the canonical-reconciled path.
func (s *OrphanSweeper) Run(ctx context.Context) {
	s.deps.Logger.Info("orphan-sweeper: starting",
		zap.Duration("tick", s.deps.Tick),
		zap.Duration("pendingTTL", s.deps.PendingTTL),
		zap.Duration("uploadedTTL", s.deps.UploadedTTL),
	)
	s.deps.Metrics.Runs.Inc() // ONCE per Run invocation (per process boot)
	ticker := time.NewTicker(s.deps.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.deps.Logger.Info("orphan-sweeper: ctx cancelled, exiting", zap.Error(ctx.Err()))
			return
		case <-ticker.C:
			stats, err := s.sweep(ctx)
			if err != nil {
				// ListPending failure: skip the tick, log warn,
				// continue on next tick. Per thinker verdict: do
				// NOT abort the loop. The Runs counter is already
				// incremented for this Run invocation; Reconciled
				// is NOT incremented for failed sweeps (no rows
				// were compensated).
				s.deps.Logger.Warn("orphan-sweeper.sweep: tick failed (DB or repo error); resuming on next tick",
					zap.Error(err))
				continue
			}
			s.deps.Logger.Info("orphan-sweeper.sweep: tick completed",
				zap.Int("pending_stale", stats.PendingStale),
				zap.Int("uploaded_stale", stats.UploadedStale),
				zap.Int("pending_done", stats.PendingDone),
				zap.Int("uploaded_done", stats.UploadedDone),
				zap.Int("trash_errors", stats.TrashErrors),
				zap.Int("mark_failed_errs", stats.MarkFailedErrs),
			)
		}
	}
}

// sweep performs one canonical reconciliation cycle. It's a method
// (not a goroutine-internal closure) so future ops tooling — e.g.
// a `cmd/admin/sweep_orphans.go` subcommand — can call it on
// demand.
//
// TTL PARTITION: ListPending returns BOTH pending+uploaded stale
// past `now - min(pendingTTL, uploadedTTL)`. The returned set is
// narrowed with a per-status UpdatedUnix re-check so:
//
//   - a stale 'pending' row newer than pendingTTL is excluded
//   - a stale 'uploaded' row newer than uploadedTTL is excluded
//
// Per-row failures (Drive.Trash, MarkFailed) are isolated — the
// loop continues iterating other rows.
func (s *OrphanSweeper) sweep(ctx context.Context) (SweepStats, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-minDuration(s.deps.PendingTTL, s.deps.UploadedTTL))

	stale, err := s.deps.Repo.ListPending(ctx, cutoff)
	if err != nil {
		return SweepStats{}, fmt.Errorf("orphan-sweeper.sweep: ListPending: %w", err)
	}

	var stats SweepStats
	for _, row := range stale {
		switch row.Status {
		case "pending":
			stats.PendingStale++
			if !isStale(row.UpdatedUnix, now, s.deps.PendingTTL) {
				continue
			}
			outcome := s.compensatePending(ctx, row)
			switch outcome {
			case outcomeReconciled:
				stats.PendingDone++
				s.deps.Metrics.Reconciled.WithLabelValues(OutcomePendingTimeout).Inc()
			case outcomeError:
				stats.MarkFailedErrs++
			case outcomeIdempotentSkip:
				// Already at 'failed' (race). No metric, no stats.
			}
		case "uploaded":
			stats.UploadedStale++
			if !isStale(row.UpdatedUnix, now, s.deps.UploadedTTL) {
				continue
			}
			outcome, trashErr := s.compensateUploaded(ctx, row)
			switch outcome {
			case outcomeReconciled:
				stats.UploadedDone++
				s.deps.Metrics.Reconciled.WithLabelValues(OutcomeUploadedCleanup).Inc()
			case outcomeError:
				stats.MarkFailedErrs++
			}
			if trashErr {
				stats.TrashErrors++
			}
		default:
			// Should not happen (ListPending filter excludes these)
			// but a defensive log keeps noise on the operator's
			// dashboard if a future refactor widens the filter.
			s.deps.Logger.Warn("orphan-sweeper.sweep: unexpected status from ListPending",
				zap.String("voiceover_id", row.VoiceoverID),
				zap.String("status", row.Status),
			)
		}
	}
	return stats, nil
}

// compensatePending marks a stale 'pending' row as failed with the
// canonical orphan_sweep reason. No Drive action (Step 2 hadn't
// completed, no Drive file exists).
func (s *OrphanSweeper) compensatePending(ctx context.Context, row UploadIntent) compensateOutcome {
	if err := s.deps.Repo.MarkFailed(ctx, row.VoiceoverID, "orphan_sweep: pending_timeout"); err != nil {
		if isUploadIntentNotFound(err) {
			// idempotent: another sweeper / manual intervention
			// already moved this row to 'failed'.
			return outcomeIdempotentSkip
		}
		s.deps.Logger.Warn("orphan-sweeper.compensatePending: MarkFailed returned error",
			zap.String("voiceover_id", row.VoiceoverID),
			zap.Error(err),
		)
		return outcomeError
	}
	return outcomeReconciled
}

// compensateUploaded moves the orphan Drive file to trash (NOT
// permanent Delete — operators can recover from Drive trash for
// 30 days) + marks the intent row as failed.
//
// Drive.Trash errors are tolerated: MarkFailed still fires so the
// sweeper doesn't loop on the same row indefinitely. The trash-err
// flag is returned separately so the sweep() loop can record it
// in TrashErrors for operator visibility.
func (s *OrphanSweeper) compensateUploaded(ctx context.Context, row UploadIntent) (compensateOutcome, bool) {
	trashErr := false
	if row.DriveFileID != "" {
		if err := s.deps.DriveDeleter.Trash(ctx, row.DriveFileID); err != nil {
			s.deps.Logger.Warn("orphan-sweeper.compensateUploaded: Drive.Trash returned error; continuing to MarkFailed so sweeper doesn't loop",
				zap.String("voiceover_id", row.VoiceoverID),
				zap.String("drive_file_id", row.DriveFileID),
				zap.Error(err),
			)
			trashErr = true
		}
	} else {
		// Empty drive_file_id on an 'uploaded' row is itself an
		// invariant violation — a row can't reach 'uploaded'
		// without a Drive upload succeeding. Log loudly so
		// operators see it (and confirm via the per-row
		// TrashErrors stat).
		s.deps.Logger.Warn("orphan-sweeper.compensateUploaded: uploaded row has empty drive_file_id; cannot Drive.Trash",
			zap.String("voiceover_id", row.VoiceoverID),
		)
		trashErr = true
	}

	if err := s.deps.Repo.MarkFailed(ctx, row.VoiceoverID, "orphan_sweep: uploaded_no_finalize"); err != nil {
		if isUploadIntentNotFound(err) {
			return outcomeIdempotentSkip, trashErr
		}
		s.deps.Logger.Warn("orphan-sweeper.compensateUploaded: MarkFailed returned error",
			zap.String("voiceover_id", row.VoiceoverID),
			zap.Error(err),
		)
		return outcomeError, trashErr
	}
	return outcomeReconciled, trashErr
}

// isStale checks whether a row's UpdatedUnix is older than
// now-ttl. We use Unix-seconds comparison directly to avoid
// timezone shenanigans (the schema stores Unix seconds per A/2).
func isStale(updatedUnix int64, now time.Time, ttl time.Duration) bool {
	if updatedUnix == 0 {
		// no updated_at → can't reason about staleness (defensive:
		// a zero value would always be in the distant past; rejecting
		// it keeps the canonical "real updated_at was written"
		// invariant visible).
		return false
	}
	return updatedUnix < now.Add(-ttl).Unix()
}

// isUploadIntentNotFound delegates to the canonical helper in
// upload_intent.go (isMarkNotFoundError) — both check the same
// ErrUploadIntentNotFound sentinel declared there.
func isUploadIntentNotFound(err error) bool {
	return isMarkNotFoundError(err)
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
