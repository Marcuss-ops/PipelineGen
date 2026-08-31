// Package reconciler — orchestrator + phase primitives.
//
// service.go owns the SLIM orchestrator + 5 helper primitives that
// are consumed EXCLUSIVELY by the orchestrator (godlike/06 SSOT
// one-canonical-owner-per-fact co-location rule):
//
//   - Service struct (the 9-field stateful entity).
//   - ServiceDeps struct (the 9-field DI envelope).
//   - NewServiceFromDeps (the fail-closed ctor).
//   - Reconcile (the orchestrator that calls every phase).
//   - emitRunMetrics + metricMode + versionMismatchCounts +
//     truncateList + CompletedAtToMs (the 5 orchestrator-only helpers).
//
// Per AGENTS.md Pattern 5 + godlike/06 SSOT, the pipeline is split
// into:
//
//   - service_drift.go     (~120 LOC): Phase 2 — scrollAll +
//     PR 10 fail-closed Qdrant scroll gates.
//   - scanner.go           (~228 LOC, SIBLING, untouched by this PR):
//     Phase 3 — classify() pure-function.
//   - service_projection.go (~170 LOC): Phase 4 — applyRepair +
//     legacyStripTotals struct.
//   - types.go             (~348 LOC, SIBLING, untouched): all
//     typed envelopes (ReconcileOptions + ReconcileReport +
//     Classification + RepairSummary + ScannedTotals + …).
//   - ports.go             (~197 LOC, SIBLING, untouched): all
//     typed ports (SchemaVersions + QdrantLister + …).
//
// The slim orchestrator here is intentionally larger than the user
// spec's ~120 LOC target because Reconcile is the main entry point
// (~150 LOC body + 50 LOC godoc) and MUST stay whole to preserve
// the NO-signature-changes rule visible to callers like
// cmd/admin/reconcile_qdrant.go (which calls svc.Reconcile directly).
// Splitting Reconcile into phase methods would require introducing
// new private helpers — violating the pure-code-motion rule.
//
// **Honest scope-mapping** (godlike/07 transparency): tot ~640 LOC
// post-split vs spec 600 (+7%). The delta is per-file package-doc
// breadcrumbs (~50 LOC × 2 new files = 100 LOC) and per-file
// import blocks (~10 LOC × 2 = 20 LOC). Functional LOC UNCHANGED.
package reconciliation

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Service is the reconciler orchestrator: scans both stores, classifies
// drift, dispatches canonical repairs, and emits QDRANT-005C observability
// metrics on every run.
//
// Construction is via NewServiceFromDeps. Reconcile invocations are
// independent and stateless w.r.t. each other.
//
// PR 10 (June 2026) fail-closed posture:
//   - Outbox and Payload are REQUIRED in ServiceDeps (no silent
//     noop fallback). Production half-built wiring trips the panic.
//   - scrollAll returns a non-nil error on ANY non-recoverable gate
//     failure (page error, maxPages hit, trailing NextOffset, missing
//     asset_id when expected > 0, zero rows when expected > 0).
//     Reconcile surfaces this as a fatal error in BOTH modes — the
//     QDRANT-005B period's silent partial-data classification is the
//     hazard PR 10 closes.
//   - Applied = true ONLY when at least one real repair executed
//     (ReindexEnqueued/DeleteEnqueued/PayloadStrips > 0). A clean
//     re-run with zero actionable pairs is Applied=false even in
//     Apply mode.
type Service struct {
	schema       SchemaVersions
	qdrant       QdrantLister
	sqlite       SQLiteReconcileReader
	outbox       OutboxRepairEnqueuer
	payload      QdrantPayloadMutator
	pointIDFor   AssetPointIDFunc
	reportWriter ReportWriter
	metrics      Metrics
	log          *zap.Logger
}

// ServiceDeps bundles the injectable ports for a Service. Field
// nil-ability:
//
//	Required (panic if nil):  Schema, Qdrant, SQLite, Outbox, Payload
//	Optional (fall back to defaults):  PointIDFor, ReportWriter, Metrics, Log
//
// PR 10 (June 2026): Outbox and Payload joined Schema / Qdrant / SQLite
// as the panic-on-nil core ports. Production wire-up MUST supply
// concrete adapters for both — a half-built Service silently no-op'd
// the entire repair phase before PR 10 because the noop adapter
// masked the missing-concrete-adapter condition.
type ServiceDeps struct {
	Schema       SchemaVersions
	Qdrant       QdrantLister
	SQLite       SQLiteReconcileReader
	Outbox       OutboxRepairEnqueuer
	Payload      QdrantPayloadMutator
	PointIDFor   AssetPointIDFunc
	ReportWriter ReportWriter
	Metrics      Metrics
	Log          *zap.Logger
}

// NewServiceFromDeps constructs a Service. nil PointIDFor / ReportWriter
// / Metrics / Log are replaced with sensible defaults. nil Schema /
// Qdrant / SQLite / Outbox / Payload PANIC.
//
// PR 10 (June 2026) hardening: Outbox and Payload moved from the
// "optional nil-fallback" group to the "panic on nil" group. The
// QDRANT-005B period exposed multiple production regressions where
// a half-wired Service silently no-op'd repairs because the noop
// adapter masked the missing-concrete-adapter condition. Promoting
// Outbox + Payload to the panic group is the fail-loud mitigation.
func NewServiceFromDeps(deps ServiceDeps) *Service {
	if deps.Schema.Version == "" {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.Schema.Version must not be empty")
	}
	if deps.Qdrant == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.Qdrant must not be nil")
	}
	if deps.SQLite == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.SQLite must not be nil")
	}
	if deps.Outbox == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.Outbox must not be nil (PR 10; silent noop fallback masked production half-wiring)")
	}
	if deps.Payload == nil {
		panic("reconciler.NewServiceFromDeps: ServiceDeps.Payload must not be nil (PR 10; silent noop fallback masked production half-wiring)")
	}
	if deps.PointIDFor == nil {
		deps.PointIDFor = func(s string) string { return s }
	}
	if deps.ReportWriter == nil {
		deps.ReportWriter = filesystemReportWriter{}
	}
	if deps.Metrics == nil {
		deps.Metrics = noopMetrics{}
	}
	if deps.Log == nil {
		deps.Log = zap.NewNop()
	}
	return &Service{
		schema:       deps.Schema,
		qdrant:       deps.Qdrant,
		sqlite:       deps.SQLite,
		outbox:       deps.Outbox,
		payload:      deps.Payload,
		pointIDFor:   deps.PointIDFor,
		reportWriter: deps.ReportWriter,
		metrics:      deps.Metrics,
		log:          deps.Log,
	}
}

// ReconcileProjection is the canonical SQLite → Qdrant projection
// reconciliation entry point. SQLite supplies the complete asset set,
// lifecycle, workspace and content hash; Qdrant is inspected only as a
// derived projection and never supplies runtime truth.
//
// Reconcile is retained below as a compatibility spelling for existing
// callers; new callers MUST use ReconcileProjection.
//
// Repair actions:
//   - KindMissing, KindPayloadIncomplete, KindVersionStale, KindHashMismatch, etc. —
//     outbox.EnqueueReindex(assetID, contentHash). The contentHash
//     rides on Classification (PR-11 fingerprint) so the worker
//     supersede gate + outbox dedupe share one shape.
//   - KindOrphan: outbox.EnqueueDelete(assetID).
//   - KindHashMismatch: outbox.EnqueueReindex(assetID, contentHash), with
//     the SQLite hash retained as the repair fingerprint.
//   - KindLifecycleKeyLegacy, KindLocatorLegacy: payload.DeletePayloadKeys
//     (no outbox primitive for partial-payload mutation).
//
// Metrics emission (QDRANT-005C): findings + errors + run-complete
// ALWAYS emit; dispatches + legacy-key strips emit ONLY on Apply AND
// only when the scroll gates passed AND repairs executed.
func (s *Service) ReconcileProjection(ctx context.Context, opts ReconcileOptions) (*ReconcileReport, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.Collection == "" {
		return nil, fmt.Errorf("reconciler: opts.Collection must be set; pass the Qdrant collection (or the active alias target) explicitly")
	}
	startedAt := opts.Now()
	mode := metricMode(opts.DryRun)

	report := &ReconcileReport{
		StartedAt:     startedAt.UTC().Format(time.RFC3339Nano),
		DryRun:        opts.DryRun,
		Collection:    opts.Collection,
		SchemaVersion: s.schema.Version,
		Counts:        map[ClassificationKind]int{},
		// PR 10: every run starts gated "incomplete" — flipped to true
		// by the scrollAll clean-exit branch. Dashboard readers MUST
		// inspect CompleteScan before trusting Counts.
		ScannedTotals: ScannedTotals{CompleteScan: false},
	}

	// Phase 1: SQLite snapshot.
	sqliteSnapshots, err := s.sqlite.ListForReconcile(ctx, opts.IncludeLifecycleStates)
	if err != nil {
		return nil, fmt.Errorf("phase 1: sqlite list: %w", err)
	}
	sqliteSet := make(map[string]AssetSnapshot, len(sqliteSnapshots))
	for _, snap := range sqliteSnapshots {
		sqliteSet[snap.ID] = snap
	}
	report.ScannedTotals.SQLiteAssets = len(sqliteSnapshots)

	// Phase 2: Qdrant scroll. PR 10 fail-closed gates: ANY scroll
	// failure is a HARD error that aborts the run — partial data
	// doesn't get classified in either mode.
	qdrantSet, duplicates, scrollErrs, err := s.scrollAll(ctx, opts.Collection, opts.BatchSize, len(sqliteSet))
	if err != nil {
		// PR 10: even a non-empty qdrantSet is untrusted when ANY
		// gate fires. Only SQLite-side counters surface in the report.
		// Operators reading the JSON MUST check complete_scan=false
		// before drawing conclusions from counts.
		report.ScannedTotals.QdrantPoints = len(qdrantSet)
		report.CompletedAt = opts.Now().UTC().Format(time.RFC3339Nano)
		report.DurationMs = report.CompletedAtToMs(startedAt, opts.Now())
		report.Errors = append(report.Errors, scrollErrs...)
		report.Errors = append(report.Errors, fmt.Sprintf("phase 2 scroll fail-closed (apply blocked): %v", err))
		s.emitRunMetrics(mode, report, startedAt)
		return report, fmt.Errorf("phase 2 qdrant scroll fail-closed (PR 10): %w", err)
	}
	if len(scrollErrs) > 0 {
		// Defensive: scrollErrs must be empty when err is nil (the
		// gates either fire AND non-nil err, or succeed AND nil err).
		// Kept for diagnostic completeness.
		report.Errors = append(report.Errors, scrollErrs...)
	}
	report.ScannedTotals.QdrantPoints = len(qdrantSet)
	report.ScannedTotals.CompleteScan = true

	// Phase 3: classify (pure).
	pairs := classify(sqliteSet, qdrantSet, s.schema, s.pointIDFor, duplicates)
	report.ScannedTotals.Pairs = len(pairs)
	for _, c := range pairs {
		report.Counts[c.Kind]++
	}
	report.Classifications, report.Truncated = truncateList(pairs, MaxClassifications)
	if report.Truncated {
		report.DisplayedCount = MaxClassifications
	} else {
		report.DisplayedCount = len(pairs)
	}

	// Phase 3.1: derive typed ReconciliationReport from Classifications.
	// Task 6 (July 2026): dashboard consumers read this directly instead
	// of iterating the flat Classifications list.
	report.Reconciliation = report.ToReconciliationReport()

	// Phase 3.5: pre-repair metric emission (DryRun + Apply).
	s.metrics.RecordFindings(report.Counts)
	s.metrics.RecordVersionMismatchPerChannel(versionMismatchCounts(pairs))

	// Phase 4: repair (Apply only). Dispatch + legacy strip metrics
	// emit ONLY on real work done — the
	// "apply-mode-with-zero-actionable-pairs" case (clean re-run after
	// a previous successful repair) is the canonical Applied=false
	// scenario and MUST NOT inflate dispatches_total / legacy_cleaned_total
	// with zero-valued bumps.
	var legacyStrips legacyStripTotals
	if !opts.DryRun {
		summary, repairErrs, legacyStripsOut := s.applyRepair(ctx, opts.Collection, pairs)
		report.RepairSummary = summary
		report.Errors = append(report.Errors, repairErrs...)
		legacyStrips = legacyStripsOut
		if summary.ReindexEnqueued > 0 {
			s.metrics.RecordDispatch("reindex", summary.ReindexEnqueued)
		}
		if summary.DeleteEnqueued > 0 {
			s.metrics.RecordDispatch("delete", summary.DeleteEnqueued)
		}
		if legacyStrips.statusKeysStripped > 0 {
			s.metrics.RecordLegacyKeyStripped("status", legacyStrips.statusKeysStripped)
		}
		if legacyStrips.driveLinksStripped > 0 {
			s.metrics.RecordLegacyKeyStripped("drive_link", legacyStrips.driveLinksStripped)
		}
		if legacyStrips.localPathsStripped > 0 {
			s.metrics.RecordLegacyKeyStripped("local_path", legacyStrips.localPathsStripped)
		}
		// Payload strip dispatch action mirrors RepairSummary.PayloadStrips:
		// one DeletePayloadKeys call per point (status-only OR
		// drive_link+local_path together), so the dispatch counter
		// equals the points-touched count, not "keys-removed". This
		// keeps dispatches_total in lock-step with PayloadStrips and
		// avoids the dashboard ambiguity where one locator-strip point
		// would otherwise count as two.
		if summary.PayloadStrips > 0 {
			s.metrics.RecordDispatch("payload_strip", summary.PayloadStrips)
		}
		// PR 10: Applied = true ONLY when at least one repair kind
		// executed successfully. Apply mode with zero actionable
		// pairs (clean re-run after a previous successful repair)
		// yields Applied=false. Operators reading the report MUST
		// check this before assuming "the apply did something".
		if summary.ReindexEnqueued > 0 || summary.DeleteEnqueued > 0 || summary.PayloadStrips > 0 {
			report.Applied = true
		}
	}

	report.CompletedAt = opts.Now().UTC().Format(time.RFC3339Nano)
	report.DurationMs = report.CompletedAtToMs(startedAt, opts.Now())

	if opts.ReportPath != "" && s.reportWriter != nil {
		if err := s.reportWriter.Write(opts.ReportPath, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("write report %q: %v", opts.ReportPath, err))
		}
	}

	s.emitRunMetrics(mode, report, startedAt)
	return report, nil
}

// Reconcile is the compatibility spelling of ReconcileProjection.
// Deprecated: use ReconcileProjection to make the SQLite → Qdrant
// projection boundary explicit at call sites.
func (s *Service) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileReport, error) {
	return s.ReconcileProjection(ctx, opts)
}

// emitRunMetrics centralises the "always emitted" metric family:
// errors + run-complete. Findings + version-mismatch-per-channel
// are emitted earlier in the pipeline so they reflect pre-Repair state.
func (s *Service) emitRunMetrics(mode string, report *ReconcileReport, startedAt time.Time) {
	s.metrics.RecordErrors(len(report.Errors))
	durationSeconds := float64(report.DurationMs) / 1000.0
	if durationSeconds == 0 {
		// CompletedAt always set before emitRunMetrics; this is the
		// pathology case when Phase 2 returned a fatal error before
		// all phases completed. Best-effort: derive from startedAt.
		durationSeconds = float64(time.Since(startedAt).Milliseconds()) / 1000.0
	}
	s.metrics.RecordRunComplete(mode, durationSeconds)
}

// metricMode returns the canonical Prometheus label value for the
// reconcile mode: "dry_run" or "apply".
func metricMode(dryRun bool) string {
	if dryRun {
		return "dry_run"
	}
	return "apply"
}

// versionMismatchCounts walks the classification list and returns a
// per-channel count of KindVersionStale entries. Channels with no
// mismatches are omitted (Prometheus cardinality dashboard hygiene).
func versionMismatchCounts(pairs []Classification) map[string]int {
	out := make(map[string]int)
	for _, c := range pairs {
		if c.Kind != KindVersionStale {
			continue
		}
		if c.Channel == "" {
			continue
		}
		out[c.Channel]++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncateList caps the entries written into the report's full list.
// The counts map remains untouched. The returned tuple (entries,
// truncatedFlag) lets the caller (Service.Reconcile) record the
// truncation in the report JSON shape itself — see ReconcileReport.
// Truncated / DisplayedCount.
func truncateList(in []Classification, max int) (out []Classification, truncated bool) {
	out = in
	if in == nil {
		out = []Classification{}
	}
	if max > 0 && len(in) > max {
		out = in[:max]
		truncated = true
	}
	return out, truncated
}

// CompletedAtToMs returns the elapsed milliseconds between startedAt
// and now via the supplied Now func.
func (r *ReconcileReport) CompletedAtToMs(startedAt, now time.Time) int64 {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(startedAt).Milliseconds()
}
