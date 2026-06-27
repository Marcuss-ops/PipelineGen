package reconciler

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
//	Required (panic if nil):  Schema, Qdrant, SQLite
//	Optional (fall back to defaults):  Outbox, Payload, PointIDFor,
//	                                   ReportWriter, Metrics, Log
//
// Optional fields fall back to no-op implementations so test callers
// can drop them with no wire-up cost. Production wire-up MUST supply
// concrete adapters for Outbox + Payload when running in Apply mode
// (nil → repairs silently no-op).
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

// NewServiceFromDeps constructs a Service. nil-with-default-fallback
// fields are replaced with no-op / zero-cost defaults (ReportWriter ->
// filesystemReportWriter{}, Metrics -> noopMetrics{}, Log ->
// zap.NewNop(), Outbox -> noopOutboxEnqueuer{}, Payload ->
// noopPayloadMutator{}, PointIDFor -> identity).
//
// Panics if Schema.Version == "", Qdrant == nil, or SQLite == nil
// (the three ports that ANY reconcile run requires). The panic is a
// fail-loud guard against production wire-up silently using a
// half-built Service.
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
		deps.Outbox = noopOutboxEnqueuer{}
	}
	if deps.Payload == nil {
		deps.Payload = noopPayloadMutator{}
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

// Reconcile runs the full scan + classify + (optional) repair pipeline.
//
// Returns the populated report. An error is returned ONLY for
// infrastructure failures (e.g. SQLite unreachable, Qdrant scroll
// page error preventing the cursor from advancing). Classification
// findings are non-fatal and surface in report.Errors + Counts.
//
// Repair actions:
//   - KindMissing, KindPayloadIncomplete, KindVersionStale,
//     KindLifecycleMismatch, KindWorkspaceMismatch, KindNonCanonicalPointID:
//     outbox.EnqueueReindex (idempotency via event_key).
//   - KindOrphan: outbox.EnqueueDelete (idempotency on assetID).
//   - KindLifecycleKeyLegacy, KindLocatorLegacy:
//     payload.DeletePayloadKeys (bundled by asset_id when feasible;
//     drops "status" / "drive_link" / "local_path" from affected
//     points). NO outbox enqueue: legacy-key stripping is a direct
//     Qdrant mutation, justified by the lack of an outbox primitive
//     for partial payload updates.
//
// Dry-run mode: repair actions are NOT dispatched, but the
// RepairSummary counts reflect what WOULD have been dispatched.
// Applied is false in dry-run, true in apply mode.
//
// Metrics emission (QDRANT-005C): findings + version-mismatch-per-channel
// + errors + run-complete ALWAYS emit; dispatches + legacy-key strips
// emit ONLY on Apply so dashboards can distinguish "scan ran" from
// "repairs ran".
func (s *Service) Reconcile(ctx context.Context, opts ReconcileOptions) (*ReconcileReport, error) {
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
		ScannedTotals: ScannedTotals{},
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

	// Phase 2: Qdrant scroll.
	qdrantSet, scrollErrs, err := s.scrollAll(ctx, opts.Collection, opts.BatchSize)
	if err != nil {
		// Infrastructure-level stop (e.g. zero points scrolled + first
		// page error). Return what we have so far + emit metrics.
		report.CompletedAt = opts.Now().UTC().Format(time.RFC3339Nano)
		report.DurationMs = report.CompletedAtToMs(startedAt, opts.Now())
		report.Errors = append(report.Errors, scrollErrs...)
		report.Errors = append(report.Errors, fmt.Sprintf("phase 2 scroll fatal: %v", err))
		s.emitRunMetrics(mode, report, startedAt)
		return report, fmt.Errorf("phase 2 qdrant scroll: %w", err)
	}
	if len(scrollErrs) > 0 {
		report.Errors = append(report.Errors, scrollErrs...)
	}
	report.ScannedTotals.QdrantPoints = len(qdrantSet)

	// Phase 3: classify (pure).
	pairs := classify(sqliteSet, qdrantSet, s.schema, s.pointIDFor)
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

	// Phase 3.5: pre-repair metric emission (DryRun + Apply).
	// Findings + version-mismatch-per-channel emit regardless of mode.
	s.metrics.RecordFindings(report.Counts)
	s.metrics.RecordVersionMismatchPerChannel(versionMismatchCounts(pairs))

	// Phase 4: repair (Apply only). Dispatch metric emission lives
	// inside applyRepair so the per-key legacy strip count is in scope.
	var legacyStrips legacyStripTotals
	if !opts.DryRun {
		summary, repairErrs, legacyStripsOut := s.applyRepair(ctx, opts.Collection, pairs)
		report.RepairSummary = summary
		report.Errors = append(report.Errors, repairErrs...)
		report.Applied = true
		legacyStrips = legacyStripsOut
		s.metrics.RecordDispatch("reindex", summary.ReindexEnqueued)
		s.metrics.RecordDispatch("delete", summary.DeleteEnqueued)
		s.metrics.RecordLegacyKeyStripped("status", legacyStrips.statusKeysStripped)
		s.metrics.RecordLegacyKeyStripped("drive_link", legacyStrips.driveLinksStripped)
		s.metrics.RecordLegacyKeyStripped("local_path", legacyStrips.localPathsStripped)
		// Payload strip dispatch action mirrors RepairSummary.PayloadStrips:
		// one DeletePayloadKeys call per point (status-only OR
		// drive_link+local_path together), so the dispatch counter
		// equals the points-touched count, not "keys-removed". This
		// keeps dispatches_total in lock-step with PayloadStrips and
		// avoids the dashboard ambiguity where one locator-strip point
		// would otherwise count as two.
		s.metrics.RecordDispatch("payload_strip", summary.PayloadStrips)
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

// emitRunMetrics centralises the "always emitted" metric family:
// errors + run-complete (which sets last_success + duration histogram).
// Findings + version-mismatch-per-channel are emitted earlier in the
// pipeline so they reflect pre-Repair state.
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
}// legacyStripTotals tracks legacy-key cleanups per key observed at
// scan time (not per DeletePayloadKeys call).
//
// INVARIANT: driveLinksStripped and localPathsStripped are NOT
// necessarily equal. They reflect payload content at scan time:
//   - driveLinksStripped = points whose payload actually carried
//     drive_link at scan time.
//   - localPathsStripped = points whose payload actually carried
//     local_path at scan time.
// The Qdrant REST DeletePayloadKeys call passes both keys because
// the REST shape accepts a list per call (no per-point granularity),
// but the on-wire cost of stripping both keys at once is identical
// to stripping one — keeping the call shape unchanged preserves
// cleanup throughput while letting dashboards tell apart
// "drive_link-only legacy migrations" from "both-keys legacy".
//
// statusKeysStripped counts LifecycleKeyLegacy points — these come
// from a SEPARATE DeletePayloadKeys call with key=["status"],
// distinct from the locator call.
//
// All three counters are bumped based on what the scanner observed,
// never per DeletePayloadKeys call.
type legacyStripTotals struct {
	statusKeysStripped int
	driveLinksStripped int
	localPathsStripped int
}

// scrollAll drives the Qdrant scroll API to completion. Returns a
// point-id-keyed map (asset_id from payload) plus a slice of non-fatal
// errors (so the caller can surface them in the report even when the
// overall run succeeded).
func (s *Service) scrollAll(ctx context.Context, collection string, batchSize int) (map[string]pointWithID, []string, error) {
	out := make(map[string]pointWithID)
	var errs []string
	offset := ""
	const maxPages = 400 // safety cap (~200k points at batch=500)
	for i := 0; i < maxPages; i++ {
		page, err := s.qdrant.ScrollPoints(ctx, collection, offset, batchSize)
		if err != nil {
			errs = append(errs, fmt.Sprintf("scroll page %d: %v", i, err))
			// Hard stop on cursor failure; what we have may be
			// partial but the caller can still classify against it.
			if len(out) == 0 {
				return nil, errs, fmt.Errorf("first scroll page failed: %w", err)
			}
			return out, errs, nil
		}
		if len(page.Items) == 0 {
			break
		}
		for _, p := range page.Items {
			assetID, _ := p.Payload["asset_id"].(string)
			if assetID == "" {
				// Surface missing canonical key as a non-fatal
				// diagnostic — do NOT poison the map with an
				// empty-string key (verifier.go::VerifyReindex
				// has the same defense).
				errs = append(errs, fmt.Sprintf("page %d: point %q payload missing asset_id", i, p.ID))
				continue
			}
			out[assetID] = pointWithID{ID: p.ID, Payload: p.Payload}
		}
		if page.NextOffset == "" {
			break
		}
		offset = page.NextOffset
		if i == maxPages-1 {
			errs = append(errs, fmt.Sprintf("scroll iteration cap %d reached; remaining points unsampled", maxPages))
		}
	}
	return out, errs, nil
}

// applyRepair dispatches the repair actions. Returns the run summary
// plus any non-fatal dispatch errors (so the report can include them
// without aborting mid-run).
//
// Per-kind dispatch policy:
//   - KindMissing, KindVersionStale, KindPayloadIncomplete,
//     KindLifecycleMismatch, KindWorkspaceMismatch,
//     KindNonCanonicalPointID: outbox.EnqueueReindex(assetID, contentHash).
//     One outbox row per classification; idempotency via event_key
//     collapses into a single round-trip per asset.
//   - KindOrphan: outbox.EnqueueDelete(assetID).
//   - KindLifecycleKeyLegacy: payload.DeletePayloadKeys([collection],
//     ["status"], [pointIDs]). All classified points in a single
//     batched payload/delete call.
//   - KindLocatorLegacy: payload.DeletePayloadKeys([collection],
//     ["drive_link", "local_path"], [pointIDs]). One batch; the keys
//     argument drives the Qdrant REST /points/payload/delete shape.
//
// Returns legacyStripTotals so the caller can emit
// payload_legacy_cleaned_total{legacy_key=...} per kind.
func (s *Service) applyRepair(ctx context.Context, collection string, pairs []Classification) (RepairSummary, []string, legacyStripTotals) {
	summary := RepairSummary{}
	var errs []string
	var legacyStrips legacyStripTotals

	var legacyKeyPoints []string
	var locatorPoints []string
	var locatorKeysByPointID = map[string][]string{}

	for _, c := range pairs {
		switch c.Kind {
		case KindMissing,
			KindVersionStale,
			KindPayloadIncomplete,
			KindLifecycleMismatch,
			KindWorkspaceMismatch,
			KindNonCanonicalPointID:
			if err := s.outbox.EnqueueReindex(ctx, c.AssetID); err != nil {
				errs = append(errs, fmt.Sprintf("enqueue reindex %s: %v", c.AssetID, err))
				continue
			}
			summary.ReindexEnqueued++

		case KindOrphan:
			if err := s.outbox.EnqueueDelete(ctx, c.AssetID); err != nil {
				errs = append(errs, fmt.Sprintf("enqueue delete %s: %v", c.AssetID, err))
				continue
			}
			summary.DeleteEnqueued++

		case KindLifecycleKeyLegacy:
			if c.QdrantPointID != "" {
				legacyKeyPoints = append(legacyKeyPoints, c.QdrantPointID)
			}
		case KindLocatorLegacy:
			if c.QdrantPointID != "" {
				locatorPoints = append(locatorPoints, c.QdrantPointID)
				locatorKeysByPointID[c.QdrantPointID] = c.LocatorKeys
			}
		}
	}

	if len(legacyKeyPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"status"}, legacyKeyPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy status keys: %v", err))
		} else {
			summary.PayloadStrips += len(legacyKeyPoints)
			legacyStrips.statusKeysStripped += len(legacyKeyPoints)
		}
	}
	if len(locatorPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"drive_link", "local_path"}, locatorPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy locator keys: %v", err))
		} else {
			summary.PayloadStrips += len(locatorPoints)
			// Per-key metric bumps use c.LocatorKeys from scan time
			// (NOT a blanket bump per point) so dashboards see
			// "drive_link-only legacy migrations" distinct from
			// "both-keys legacy" — see legacyStripTotals doc on the
			// driveLinksStripped vs localPathsStripped invariant.
			for _, keys := range locatorKeysByPointID {
				for _, k := range keys {
					switch k {
					case "drive_link":
						legacyStrips.driveLinksStripped++
					case "local_path":
						legacyStrips.localPathsStripped++
					}
				}
			}
		}
	}

	return summary, errs, legacyStrips
}

// truncateList caps the entries written into the report's full list.
// The counts map remains untouched. The returned tuple (entries,
// truncatedFlag) lets the caller (Service.Reconcile) record the
// truncation in the report JSON shape itself — see ReconcileReport.
//Truncated / DisplayedCount.
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
