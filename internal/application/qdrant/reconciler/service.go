package reconciler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Service is the reconciler orchestrator: scans both stores, classifies
// drift, dispatches canonical repairs. Construction is via NewService;
// reconcile invocations are independent and stateless w.r.t. each other.
type Service struct {
	schema       SchemaVersions
	qdrant       QdrantLister
	sqlite       SQLiteReconcileReader
	outbox       OutboxRepairEnqueuer
	payload      QdrantPayloadMutator
	pointIDFor   AssetPointIDFunc
	reportWriter ReportWriter
	log          *zap.Logger
}

// NewService constructs a Service. nil reportWriter is replaced with
// filesystemReportWriter{} (writes to optionally-supplied ReportPath).
// nil log falls back to a no-op logger.
func NewService(
	schema SchemaVersions,
	qdrant QdrantLister,
	sqlite SQLiteReconcileReader,
	outbox OutboxRepairEnqueuer,
	payload QdrantPayloadMutator,
	pointIDFor AssetPointIDFunc,
	reportWriter ReportWriter,
	log *zap.Logger,
) *Service {
	if reportWriter == nil {
		reportWriter = filesystemReportWriter{}
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		schema:       schema,
		qdrant:       qdrant,
		sqlite:       sqlite,
		outbox:       outbox,
		payload:      payload,
		pointIDFor:   pointIDFor,
		reportWriter: reportWriter,
		log:          log,
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
		// page error). Return what we have so far.
		report.CompletedAt = opts.Now().UTC().Format(time.RFC3339Nano)
		report.DurationMs = report.CompletedAtToMs(startedAt, opts.Now())
		report.Errors = append(report.Errors, scrollErrs...)
		report.Errors = append(report.Errors, fmt.Sprintf("phase 2 scroll fatal: %v", err))
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
	report.Classifications = truncateList(pairs, MaxClassifications)

	// Phase 4: repair.
	if !opts.DryRun {
		summary, repairErrs := s.applyRepair(ctx, opts.Collection, pairs)
		report.RepairSummary = summary
		report.Errors = append(report.Errors, repairErrs...)
		report.Applied = true
	}

	report.CompletedAt = opts.Now().UTC().Format(time.RFC3339Nano)
	report.DurationMs = report.CompletedAtToMs(startedAt, opts.Now())

	if opts.ReportPath != "" && s.reportWriter != nil {
		if err := s.reportWriter.Write(opts.ReportPath, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("write report %q: %v", opts.ReportPath, err))
		}
	}

	return report, nil
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
func (s *Service) applyRepair(ctx context.Context, collection string, pairs []Classification) (RepairSummary, []string) {
	summary := RepairSummary{}
	var errs []string

	var legacyKeyPoints []string
	var locatorPoints []string

	for _, c := range pairs {
		switch c.Kind {
		case KindMissing,
			KindVersionStale,
			KindPayloadIncomplete,
			KindLifecycleMismatch,
			KindWorkspaceMismatch,
			KindNonCanonicalPointID:
			contentHash := s.lookupContentHash(c.AssetID)
			if contentHash == "" {
				contentHash = "reconcile-repair:" + c.Kind + ":" + c.AssetID
			}
			if err := s.outbox.EnqueueReindex(ctx, c.AssetID, contentHash); err != nil {
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
			}
		}
	}

	if len(legacyKeyPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"status"}, legacyKeyPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy status keys: %v", err))
		} else {
			summary.PayloadStrips += len(legacyKeyPoints)
		}
	}
	if len(locatorPoints) > 0 {
		if err := s.payload.DeletePayloadKeys(ctx, collection, []string{"drive_link", "local_path"}, locatorPoints); err != nil {
			errs = append(errs, fmt.Sprintf("strip legacy locator keys: %v", err))
		} else {
			summary.PayloadStrips += len(locatorPoints)
		}
	}

	return summary, errs
}

// lookupContentHash returns the most-recent content hash for an
// asset_id, if available. Production wire-up pulls this from the
// SQLite snapshot side-table or from the asset store. The default
// impl here returns "" and forces the dispatcher to use the
// reconcile-shaped fallback hash, which the outbox ON CONFLICT
// still treats as idempotent because the asset_id is a stable
// half of the event_key.
//
// Subclasses (e.g. production wire-up in cmd/admin) may override by
// wrapping the OutboxRepairEnqueuer to inject ContentHash.
func (s *Service) lookupContentHash(assetID string) string {
	return ""
}

// truncateList caps the entries written into the report's full list.
// The counts map remains untouched.
func truncateList(in []Classification, max int) []Classification {
	if max <= 0 || len(in) <= max {
		if in == nil {
			return []Classification{}
		}
		return in
	}
	return in[:max]
}

// CompletedAtToMs returns the elapsed milliseconds between startedAt
// and now via the supplied Now func.
func (r *ReconcileReport) CompletedAtToMs(startedAt, now time.Time) int64 {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(startedAt).Milliseconds()
}
