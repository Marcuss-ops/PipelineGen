// Package reconciler — service.go (Blocco 3.2 commit 2/2, June 2026)
//
// Service is the deletion-reconciler orchestrator. Mirrors the
// qdrant/reconciler/Service shape with explicit-Required-port
// nil-panic pattern (PR-10 holy principle: production half-built
// wiring must trip a build-time panic rather than silently no-op
// the dispatch phase).
//
// Flow per tick:
//
//	Phase 1 — Load: sql.ListStuckRows(now-threshold).
//	Phase 2 — Classify: pure func classify(row) → RepairAction.
//	Phase 3 — Dispatch: per row, call the outbox port. ON CONFLICT
//	                    collapses repeats at the outbox layer.
//	Phase 4 — Metric: bump counters {requeue_drive, requeue_index,
//	                              skipped, errored} per outcome.
//
// Ticker-driven via Service.Run(ctx). Run loops on a goroutine
// until ctx.Done(); cancellation drops the loop without a final
// flush (the next worker startup or admin command picks up any
// in-flight stuck rows).
package assets

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Service is the deletion-reconciler orchestrator. Construction
// is via NewServiceFromDeps.
type Service struct {
	scanner    Scanner
	enqueuer   OutboxEnqueuer
	metrics    Metrics
	clock      func() time.Time
	log        *zap.Logger
	defaultInt time.Duration
	defaultThr time.Duration
}

// ServiceDeps bundles the injectable ports. Field nil-ability:
//
//	Required (panic if nil at construction):
//	  - Scanner
//	  - OutboxEnqueuer
//	Optional (fall back to defaults):
//	  - Metrics    → noopMetrics (zero bumps)
//	  - Clock      → time.Now
//	  - Log        → zap.NewNop
type ServiceDeps struct {
	Scanner        Scanner
	OutboxEnqueuer OutboxEnqueuer
	Metrics        Metrics
	Clock          func() time.Time
	Log            *zap.Logger

	// DefaultInterval + DefaultThreshold are the ticker sleep period
	// and stuck-row detection cutoff respectively. Production
	// composition wires them from cfg.Jobs (DeletionReconcilerInterval
	// = 15m, DeletionReconcilerStuckThreshold = 30m). Defaults below
	// are sensible for dev mode.
	DefaultInterval  time.Duration
	DefaultThreshold time.Duration
}

// NewServiceFromDeps constructs a Service. nil Scanner or
// OutboxEnqueuer PANICS at construction — silent no-op dispatch
// was the canonical regression in pre-Wave-21 production wiring.
func NewServiceFromDeps(deps ServiceDeps) *Service {
	if deps.Scanner == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.Scanner must not be nil")
	}
	if deps.OutboxEnqueuer == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.OutboxEnqueuer must not be nil")
	}
	if deps.Metrics == nil {
		deps.Metrics = noopMetrics{}
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	if deps.DefaultInterval <= 0 {
		deps.DefaultInterval = 15 * time.Minute
	}
	if deps.DefaultThreshold <= 0 {
		deps.DefaultThreshold = 30 * time.Minute
	}
	return &Service{
		scanner:    deps.Scanner,
		enqueuer:   deps.OutboxEnqueuer,
		metrics:    deps.Metrics,
		clock:      deps.Clock,
		log:        deps.Log.Named("deletion-reconciler"),
		defaultInt: deps.DefaultInterval,
		defaultThr: deps.DefaultThreshold,
	}
}

// Run drives the periodic ticker loop. Returns when ctx is
// cancelled. Each tick invokes ReconcileOnce(ctx, RunOptions).
//
// Idempotent re-entry: a single Service instance may Run multiple
// times sequentially; the loop is stateless w.r.t. previous runs
// (no shared mutable state across ticks). Concurrency: at most
// ONE active tick per Service; calling Run twice on the same
// Service instance while the first is active is undefined.
func (s *Service) Run(ctx context.Context) {
	interval := s.defaultInt
	s.log.Info("deletion-reconciler: started",
		zap.Duration("interval", interval),
		zap.Duration("threshold", s.defaultThr),
	)
	t := time.NewTicker(interval)
	defer t.Stop()
	// Tick once immediately so first run isn't delayed by interval.
	s.tickOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			s.log.Info("deletion-reconciler: context cancelled, exiting")
			return
		case <-t.C:
			s.tickOnce(ctx)
		}
	}
}

// tickOnce invokes ReconcileOnce with the default options + clock.
// Errors are logged inside ReconcileOnce so tickOnce itself stays
// simple.
func (s *Service) tickOnce(ctx context.Context) {
	_, _ = s.ReconcileOnce(ctx, RunOptions{
		Now:       s.clock,
		Interval:  s.defaultInt,
		Threshold: s.defaultThr,
	})
}

// ReconcileOnce runs the 4-phase pipeline ONCE. Returns the report
// regardless of error; error is non-nil iff the Phase 1 SQL load
// fails (a transient SQL error aborts the run rather than producing
// partial-actionable data — PR-10-style fail-close on the data load).
//
// Phase 4 metrics are ALWAYS emitted (deletion_reconciler_run_complete)
// even on Phase 1 error so dashboards see the "the reconciler ran"
// signal AND the "the reconciler hit an error" signal separately.
func (s *Service) ReconcileOnce(ctx context.Context, opts RunOptions) (*RunReport, error) {
	if opts.Now == nil {
		opts.Now = s.clock
	}
	if opts.Interval <= 0 {
		opts.Interval = s.defaultInt
	}
	if opts.Threshold <= 0 {
		opts.Threshold = s.defaultThr
	}
	report := &RunReport{
		StartedAt:  opts.Now(),
		SInterval:  opts.Interval,
		SThreshold: opts.Threshold,
	}

	// Phase 1 — Load (fail-close on transient SQL error). The
	// scanner computes the `now - threshold` cutoff internally; we
	// pass now + threshold and let it own the WHERE clause.
	now := opts.Now()
	rows, err := s.scanner.ListStuckRows(now, opts.Threshold)
	if err != nil {
		report.CompletedAt = opts.Now()
		report.DurationMs = report.CompletedAt.Sub(report.StartedAt).Milliseconds()
		report.RowsErrored = 1
		report.Errors = append(report.Errors, fmt.Sprintf("phase 1 scan: %v", err))
		s.metrics.RecordErrored()
		s.metrics.RecordRunComplete(float64(report.DurationMs) / 1000.0)
		s.log.Error("deletion-reconciler: phase 1 scan failed", zap.Error(err))
		return report, fmt.Errorf("phase 1 scan: %w", err)
	}
	report.RowsScanned = len(rows)

	// Phase 2 — Classify (pure).
	results := make([]ClassifyResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, Classify(row))
	}

	// Phase 3 — Dispatch (per-row; transient errors don't abort the run).
	for _, r := range results {
		if r.Action == "" {
			report.RowsSkipped++
			s.metrics.RecordSkipped(r.Skip)
			continue
		}
		var dispatchErr error
		switch r.Action {
		case ActionRequeueDrive:
			// Safe-fallback: permanently=false (Trash route). The
			// original `permanently=true` intent is not preserved
			// on a stuck row — reconciler re-emission is
			// recovery, not user-initiated. See ports.go
			// OutboxEnqueuer docstring.
			dispatchErr = s.enqueuer.EnqueueDriveDelete(ctx, r.Row.AssetID, false)
		case ActionRequeueIndex:
			dispatchErr = s.enqueuer.EnqueueIndexDelete(ctx, r.Row.AssetID)
		default:
			// unreachable given Classify's closed switch above
			report.RowsErrored++
			s.metrics.RecordErrored()
			report.Errors = append(report.Errors, fmt.Sprintf("unknown action %q for %s", r.Action, r.Row.AssetID))
			continue
		}
		if dispatchErr != nil {
			report.RowsErrored++
			report.Errors = append(report.Errors, fmt.Sprintf("dispatch %s %s: %v", r.Action, r.Row.AssetID, dispatchErr))
			s.metrics.RecordErrored()
			s.log.Warn("deletion-reconciler: dispatch failed",
				zap.String("asset_id", r.Row.AssetID),
				zap.String("action", string(r.Action)),
				zap.Error(dispatchErr),
			)
			continue
		}
		switch r.Action {
		case ActionRequeueDrive:
			report.RowsRequeueDrive++
		case ActionRequeueIndex:
			report.RowsRequeueIndex++
		}
		s.metrics.RecordRepair(string(r.Action), r.Row.State)
		s.log.Info("deletion-reconciler: dispatched",
			zap.String("asset_id", r.Row.AssetID),
			zap.String("from_state", r.Row.State),
			zap.String("action", string(r.Action)),
		)
	}

	// Phase 4 — Final metric.
	report.CompletedAt = opts.Now()
	report.DurationMs = report.CompletedAt.Sub(report.StartedAt).Milliseconds()
	s.metrics.RecordRunComplete(float64(report.DurationMs) / 1000.0)
	s.log.Info("deletion-reconciler: tick complete",
		zap.Int("scanned", report.RowsScanned),
		zap.Int("requeue_drive", report.RowsRequeueDrive),
		zap.Int("requeue_index", report.RowsRequeueIndex),
		zap.Int("skipped", report.RowsSkipped),
		zap.Int("errored", report.RowsErrored),
		zap.Int64("duration_ms", report.DurationMs),
	)
	return report, nil
}

// noopMetrics satisfies Metrics with empty bodies so tests /
// callers that don't care about metrics don't have to plumb a
// recorder. The production composition root wires the canonical
// Prometheus-backed implementation.
type noopMetrics struct{}

func (noopMetrics) RecordRepair(string, string) {}
func (noopMetrics) RecordSkipped(string)        {}
func (noopMetrics) RecordErrored()              {}
func (noopMetrics) RecordRunComplete(float64)   {}
