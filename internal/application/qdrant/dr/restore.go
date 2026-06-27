// dr/restore.go — QDRANT-005C PR3 RestoreService.
//
// SAFETY-CRITICAL flow. The previous Qdrant Restore subsystem was
// directly callable from admin code without a post-restore integrity
// check — a partial / silently-failed restore produced a queryable
// collection with unexpected payload data, which is invisible from
// outside until operators notice wrong search results. The
// verify-then-switch gate fixes that class of outage.
//
// Pipeline (in this order):
//
//  1. SnapshotStore.GetSnapshotURL — INTERNAL RESOLUTION. The operator
//     passes the snapshot NAME; this resolves the URL via a separate
//     Qdrant REST call (so the operator is not copy-pasting URLs
//     between commands in a high-stress DR scenario).
//  2. Allocate a timestamped restore-target collection named
//     `<source>__restore_<RFC3339Nano>`. The RFC3339Nano suffix
//     guarantees zero collision risk on duplicate restores (rare in
//     practice but a manual rerun must NOT clobber an older restore
//     target mid-investigation).
//  3. SnapshotStore.RestoreSnapshot(target, url) — destructive
//     replace-in-place of the destination collection with the
//     snapshot's contents.
//  4. Verifier.VerifyReindex(target, expectedPoints) — the gate. If
//     Ready=false: candidate KEPT (operators inspect), metric emitted
//     with action="rehydrate" so dashboards alert on failed
//     rehydrations, error returned to caller. SwitchAlias NEVER runs
//     in this branch.
//  5. On Ready=true: AliasSwitcher.SwitchAlias(source, target) and the
//     qdrant_alias_current_collection gauge is set to (alias, target).
//     Old data in `source` is REACHABLE for rollback via the
//     `keep_last_n` floor in the retention sweep — dr.RetentionService
//     is the canonical post-restore cleanup.
//
// QDRANT-005C PR3 (June 2026): mirrors PR2 ServiceDeps struct pattern.
// Panics on nil Store, Switcher, Creator, or Verifier. Metrics falls
// back to noopMetrics{}. Log falls back to zap.NewNop().
package dr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// RestoreService owns the verify-then-switch pipeline.
// Construction via NewRestoreServiceFromDeps (panic-on-nil required
// ports; optional ports fall back to no-op defaults).
type RestoreService struct {
	store    SnapshotStore
	switcher AliasSwitcher
	creator  CollectionCreator
	verifier Verifier
	metrics  DRMetrics
	log      *zap.Logger
	now      func() time.Time
}

// RestoreServiceDeps bundles the injectable ports. Field nil-ability:
//
//	Required (panic if nil): Store, Switcher, Creator, Verifier
//	Optional (no-op default): Metrics, Log, Now (clock; defaults to time.Now)
type RestoreServiceDeps struct {
	Store    SnapshotStore
	Switcher AliasSwitcher
	Creator  CollectionCreator
	Verifier Verifier
	Metrics  DRMetrics
	Log      *zap.Logger
	// Now is the clock source used for the timestamped target
	// collection name. Tests inject a const; production uses time.Now.
	// If nil, defaults to time.Now via the NowFunc package var.
	Now func() time.Time
}

// NewRestoreServiceFromDeps panics on nil core ports (Store, Switcher,
// Creator, Verifier). Optional ports (Metrics / Log / Now) fall back to
// safe defaults so test fixtures can drop them with no wire-up cost.
func NewRestoreServiceFromDeps(deps RestoreServiceDeps) *RestoreService {
	if deps.Store == nil {
		panic("dr.NewRestoreServiceFromDeps: RestoreServiceDeps.Store must not be nil")
	}
	if deps.Switcher == nil {
		panic("dr.NewRestoreServiceFromDeps: RestoreServiceDeps.Switcher must not be nil")
	}
	if deps.Creator == nil {
		panic("dr.NewRestoreServiceFromDeps: RestoreServiceDeps.Creator must not be nil")
	}
	if deps.Verifier == nil {
		panic("dr.NewRestoreServiceFromDeps: RestoreServiceDeps.Verifier must not be nil")
	}
	if deps.Metrics == nil {
		deps.Metrics = noopMetrics{}
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	if deps.Now == nil {
		deps.Now = NowFunc
	}
	return &RestoreService{
		store:    deps.Store,
		switcher: deps.Switcher,
		creator:  deps.Creator,
		verifier: deps.Verifier,
		metrics:  deps.Metrics,
		log:      deps.Log,
		now:      deps.Now,
	}
}

// RestoreOptions is the input to RestoreService.Restore.
//
//	Collection:     source collection (typically the runtime alias target).
//	SnapshotName:   name of the snapshot to restore. Internal URL resolution
//	                means the operator never copies a download URL.
//	ExpectedPoints: required for the VerifyReindex gate. Negative / zero
//	                is rejected — RestoreService refuses to gate on a
//	                trivial expected count (Ready may be true trivially).
//	Alias:          runtime alias to flip on success. Empty is rejected.
type RestoreOptions struct {
	Collection     string
	SnapshotName   string
	ExpectedPoints int
	Alias          string
}

// RestoreReport is the outcome of a RestoreService.Restore. Either
// Applied=true (alias switched to Target) OR Applied=false (verifier
// blocked the switch; inspect VerifyReport.Errors to diagnose).
//
// Failure mode contract: when Applied=false, Target is KEPT on the
// Qdrant cluster so the operator can manually inspect what was
// restored. DeleteCollection on Target is a manual ops action that
// lives in CleanupWithConfig (retention sweep) or the
// `dr-qdrant apply-retention` admin subcommand.
type RestoreReport struct {
	Applied      bool          `json:"applied"`
	Source       string        `json:"source"`
	SnapshotName string        `json:"snapshot_name"`
	Target       string        `json:"target,omitempty"`
	Alias        string        `json:"alias"`
	Verify       *VerifyReport `json:"verify,omitempty"`
	DurationMs   int64         `json:"duration_ms"`
}

// Restore is the verify-then-switch entry point. Returns:
//
//   - (report, nil) with Applied=true  — alias flipped, target is live
//   - (report, nil) with Applied=false — verify blocked; candidate kept
//     (caller inspects report.Verify.Errors)
//   - (nil, err)                       — infrastructure failure
//     (snapshot URL missing, restore endpoint failed, verify threw, etc.)
//
// The metric emission policy:
//   - Verify FAIL: RecordAliasSwitch("rehydrate", duration) so dashboards
//     see the failed attempt + SetAliasCurrent keeps the OLD binding.
//   - Verify PASS: RecordAliasSwitch("rehydrate", duration) +
//     SetAliasCurrent(alias, target) for both the OLD and NEW (alias, *)
//     label sets — operators see the new target = 1 in dashboards.
func (s *RestoreService) Restore(ctx context.Context, opts RestoreOptions) (*RestoreReport, error) {
	started := s.now().UTC()

	if err := validateRestoreOptions(opts); err != nil {
		return nil, err
	}

	// 1. Resolve snapshot URL via the SnapshotStore port.
	snapshotURL, err := s.store.GetSnapshotURL(ctx, opts.Collection, opts.SnapshotName)
	if err != nil {
		return nil, fmt.Errorf("dr.RestoreService: resolve snapshot URL %q in %q: %w",
			opts.SnapshotName, opts.Collection, err)
	}

	// 2. Allocate timestamped restore-target collection.
	target := buildRestoreTarget(opts.Collection, s.now)
	if err := s.creator.CreateCollection(ctx, target); err != nil {
		return nil, fmt.Errorf("dr.RestoreService: create restore-target %q: %w", target, err)
	}
	s.log.Info("restore-target collection created",
		zap.String("source", opts.Collection),
		zap.String("target", target),
		zap.String("snapshot", opts.SnapshotName))

	// 3. Apply snapshot.
	if err := s.store.RestoreSnapshot(ctx, target, snapshotURL); err != nil {
		// Keep the target as a forensic artefact; surface error.
		s.log.Warn("restore failed at snapshot-apply step; target collection KEPT for inspection",
			zap.String("target", target), zap.Error(err))
		return nil, fmt.Errorf("dr.RestoreService: RestoreSnapshot(%q) failed; target kept at %q: %w",
			opts.SnapshotName, target, err)
	}

	// 4. Verify gate. This is the SAFETY-CRITICAL step: a partial
	// restore is invisible from outside; without this call, the alias
	// would flip to a queryable but corrupt surface.
	verifyReport, err := s.verifier.VerifyReindex(ctx, target, opts.ExpectedPoints)
	if err != nil {
		s.log.Warn("restore: verifier threw; target KEPT for inspection",
			zap.String("target", target), zap.Error(err))
		return nil, fmt.Errorf("dr.RestoreService: VerifyReindex(%q) failed; target kept: %w", target, err)
	}

	duration := float64(s.now().Sub(started).Milliseconds()) / 1000.0
	report := &RestoreReport{
		Source:       opts.Collection,
		SnapshotName: opts.SnapshotName,
		Target:       target,
		Alias:        opts.Alias,
		Verify:       verifyReport,
		DurationMs:   s.now().Sub(started).Milliseconds(),
	}

	if !verifyReport.Ready {
		// Gate blocked the switch. metric emitted with action=rehydrate
		// so dashboards see the failed attempt; the OLD (alias, source)
		// binding remains authoritative until a manual rollback via
		// the operator.
		s.metrics.RecordAliasSwitch("rehydrate", duration)
		s.metrics.SetAliasCurrent(opts.Alias, opts.Collection)
		s.log.Warn("restore: verify gate BLOCKED alias switch; candidate kept for inspection",
			zap.String("target", target),
			zap.String("alias", opts.Alias),
			zap.Strings("errors", verifyReport.Errors))
		report.Applied = false
		return report, nil
	}

	// 5. Flip the alias. This is the only branch that mutates the
	// runtime alias; alias switch failures trigger (report, err)
	// because they are infrastructure-level — caller inspects.
	if err := s.switcher.SwitchAlias(ctx, opts.Alias, opts.Collection, target); err != nil {
		s.metrics.RecordAliasSwitch("rehydrate", duration)
		// Old alias binding stays; do NOT call SetAliasCurrent(target).
		s.log.Warn("restore: alias switch failed after verified restore",
			zap.String("target", target),
			zap.String("alias", opts.Alias),
			zap.Error(err))
		return report, fmt.Errorf("dr.RestoreService: SwitchAlias(%q, %q -> %q) failed after verified restore: %w",
			opts.Alias, opts.Collection, target, err)
	}

	report.Applied = true
	s.metrics.RecordAliasSwitch("rehydrate", duration)
	s.metrics.SetAliasCurrent(opts.Alias, target)
	s.log.Info("restore: verified + aliased successfully",
		zap.String("source", opts.Collection),
		zap.String("target", target),
		zap.String("alias", opts.Alias),
		zap.Int("verified_points", verifyReport.ActualPoints),
		zap.Int64("duration_ms", report.DurationMs))
	return report, nil
}

// validateRestoreOptions applies fail-loud input validation so the
// caller can't accidentally run a no-gate restore.
func validateRestoreOptions(opts RestoreOptions) error {
	if opts.Collection == "" {
		return fmt.Errorf("dr.RestoreService.Restore: Collection must not be empty")
	}
	if opts.SnapshotName == "" {
		return fmt.Errorf("dr.RestoreService.Restore: SnapshotName must not be empty")
	}
	if opts.ExpectedPoints <= 0 {
		return fmt.Errorf("dr.RestoreService.Restore: ExpectedPoints must be > 0 (a trivial expected count defeats the Ready gate)")
	}
	if opts.Alias == "" {
		return fmt.Errorf("dr.RestoreService.Restore: Alias must not be empty (the gate is per-alias; the switch is per-alias)")
	}
	return nil
}

// buildRestoreTarget returns the canonical timestamped restore-target
// name. Format: `<source>__restore_<RFC3339Nano>`. The double underscore
// separator + RFC3339Nano (precision: nanoseconds) guarantees zero
// collision risk on rapid duplicate reruns.
//
// Sanitization: dots in the source collection name are replaced with
// underscores so the result remains a valid Qdrant collection
// identifier (Qdrant accepts `[a-zA-Z0-9_-]+`).
func buildRestoreTarget(source string, now func() time.Time) string {
	safe := strings.ReplaceAll(source, ".", "_")
	suffix := now().UTC().Format(time.RFC3339Nano)
	// RFC3339Nano embeds `:` and `T`; strip them so Qdrant accepts it.
	safeSuffix := strings.NewReplacer(":", "", "T", "", "-", "", "+", "", ".", "").Replace(suffix)
	return safe + "__restore_" + safeSuffix
}
