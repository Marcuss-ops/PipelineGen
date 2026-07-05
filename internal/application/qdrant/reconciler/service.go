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

// Reconcile runs the full scan + classify + (optional) repair pipeline.
//
// Returns the populated report.
//   - An error is returned for ANY infrastructure failure OR any PR 10
//     fail-closed scroll gate violation. The report is still populated
//     with whatever was gathered so the operator sees the partial state.
//   - report.Applied = true ONLY when every gate passed AND at least one
//     repair kind executed successfully (ReindexEnqueued/DeleteEnqueued/
//     PayloadStrips > 0).
//   - When DryRun is true, Applied stays false in all cases (no
//     dispatch happened).
//
// Repair actions:
//   - KindMissing, KindPayloadIncomplete, KindVersionStale, etc. —
//     outbox.EnqueueReindex(assetID, contentHash). The contentHash
//     rides on Classification (PR-11 fingerprint) so the worker
//     supersede gate + outbox dedupe share one shape.
//   - KindOrphan: outbox.EnqueueDelete(assetID).
//   - KindLifecycleKeyLegacy, KindLocatorLegacy: payload.DeletePayloadKeys
//     (no outbox primitive for partial-payload mutation).
//
// Metrics emission (QDRANT-005C): findings + errors + run-complete
// ALWAYS emit; dispatches + legacy-key strips emit ONLY on Apply AND
// only when the scroll gates passed AND repairs executed.
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

// legacyStripTotals tracks legacy-key cleanups per key observed at
// scan time (not per DeletePayloadKeys call).
//
// INVARIANT: driveLinksStripped and localPathsStripped are NOT
// necessarily equal. They reflect payload content at scan time:
//   - driveLinksStripped = points whose payload actually carried
//     drive_link at scan time.
//   - localPathsStripped = points whose payload actually carried
//     local_path at scan time.
//
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

// scrollAll drives the Qdrant scroll API to completion with PR 10's
// fail-closed gates. expectedAssets = len(sqliteSet) — when > 0,
// the scan is expected to yield at least one decoded Qdrant point
// AND every decoded point's payload MUST carry a non-empty
// asset_id.
//
// Fail-closed gates (each returns a non-nil error from scrollAll and
// causes Reconcile to abort the run BEFORE classifying):
//
//	(a) any scroll page error                         — fatal
//	(b) maxPages reached with more pages trailing     — fatal (capped)
//	(c) trailing NextOffset at end of clean-exit loop — fatal
//	    (defence-in-depth — reachable only if someone raises
//	    maxPages without also bounding the trailing offset)
//	(e) expectedAssets > 0 ∧ PointsMissingAssetID >= 1 — fatal
//
// PR 10 unifies these into a single hard-error return; the QDRANT-005B
// period's soft path that returned partial data with nil err is gone.
// Partial data is discarded (`out`) so the caller cannot classify
// against an incomplete picture even by accident.
//
// On clean exit: returns (out, [], nil). Caller flips
// ScannedTotals.CompleteScan = true.
//
// Note on the original "zero Qdrant points with expected > 0" gate:
// it was considered and dropped in PR 10. The reconcile-repair use
// case IS "SQLite has N rows, Qdrant has 0 matching" — blocking it
// would prevent the reconciler from doing its primary job. Operators
// who suspect a misconfiguration (wrong alias, wrong collection)
// should still rely on the count gates in the post-reindex verifier
// (PR 12) — Qdrant.CountPoints reports N above the rest API which is
// a stronger signal than scroll returning zero.
func (s *Service) scrollAll(ctx context.Context, collection string, batchSize int, expectedAssets int) (map[string]pointWithID, map[string][]pointWithID, []string, error) {
	out := make(map[string]pointWithID)
	dupes := make(map[string][]pointWithID)
	var errs []string
	var missingIDs int
	var lastNextOffset string
	const maxPages = 400 // safety cap (~200k points at batch=500)
	for i := 0; i < maxPages; i++ {
		page, err := s.qdrant.ScrollPoints(ctx, collection, lastNextOffset, batchSize)
		if err != nil {
			// Gate (a): ANY scroll page error is fatal. PR 10 closes
			// the QDRANT-005B regression that returned partial data
			// with nil err after the first page.
			errs = append(errs, fmt.Sprintf("scroll page %d: %v", i, err))
			return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: scroll page %d failed: %w", i, err)
		}
		if len(page.Items) == 0 {
			break
		}
		for _, p := range page.Items {
			assetID, ok := p.Payload["asset_id"].(string)
			if !ok || assetID == "" {
				// Gate (e) antecedent: empty/missing asset_id is a
				// canonical-decode failure. We do NOT poison the map
				// with a synthetic-key entry (would mask MissingIDs
				// later). Continue scanning so the trailing-NextOffset
				// gate can still fire independently; the gate (e)
				// check at the end rolls up the count.
				missingIDs++
				errs = append(errs, fmt.Sprintf("page %d: point %q missing asset_id (fail-closed)", i, p.ID))
				continue
			}
			// Task 6: detect duplicates — when the same asset_id already
			// exists in the output map, the new point is a duplicate.
			// The first occurrence stays in the map; subsequent ones
			// are collected for classification as KindDuplicate.
			if _, exists := out[assetID]; exists {
				// Task 6: duplicate point — same asset_id already exists
				// in the output map (first occurrence is canonical).
				// Extra copies are flagged as KindDuplicate.
				dupes[assetID] = append(dupes[assetID], pointWithID{ID: p.ID, Payload: p.Payload})
				continue
			}
			out[assetID] = pointWithID{ID: p.ID, Payload: p.Payload}
		}
		if page.NextOffset == "" {
			lastNextOffset = ""
			break
		}
		lastNextOffset = page.NextOffset
		if i == maxPages-1 {
			// Gate (b): maxPages cap hit with trailing NextOffset.
			errs = append(errs, fmt.Sprintf("scroll iteration cap %d reached with NextOffset=%q; remaining points unsampled (fail-closed)", maxPages, lastNextOffset))
			return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: maxScrolls=%d reached with NextOffset still trailing (more points unsampled)", maxPages)
		}
	}

	// Gate (c) — defence in depth. Reaching this branch means the
	// for-loop's `if page.NextOffset == "" break` did NOT fire yet
	// (i.e. every visited page returned a non-empty NextOffset). The
	// inner `i == maxPages-1` early-return above prevents this at
	// the current cap, but if the cap is ever raised without also
	// bounding the trailing offset, this branch is the safety net.
	if lastNextOffset != "" {
		errs = append(errs, fmt.Sprintf("QDRANT-fail-closed: trailing NextOffset=%q at end of scroll loop", lastNextOffset))
		return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: trailing NextOffset=%q after cursor exhaust", lastNextOffset)
	}
	// Gate (e): missing-asset_id count when expected > 0. Even if
	// some points decoded fine, the undecoded ones are unknowns —
	// surfacing ZERO Orphan IDs in that case would falsely reassure
	// the operator. Discard partial data and abort.
	if expectedAssets > 0 && missingIDs > 0 {
		errs = append(errs, fmt.Sprintf("QDRANT-fail-closed: %d points missing asset_id (canonical decode failure on %d/%d assets)", missingIDs, missingIDs, expectedAssets))
		return out, dupes, errs, fmt.Errorf("QDRANT-fail-closed: %d Qdrant points with empty/missing asset_id (canonical decode failure)", missingIDs)
	}
	return out, dupes, errs, nil
}

// applyRepair dispatches the repair actions. Returns the run summary
// plus any non-fatal dispatch errors (so the report can include them
// without aborting mid-run).
//
// Per-kind dispatch policy:
//   - KindMissing, KindVersionStale, KindPayloadIncomplete,
//     KindLifecycleMismatch, KindWorkspaceMismatch,
//     KindNonCanonicalPointID: outbox.EnqueueReindex(assetID, contentHash).
//     The contentHash is propagated from Classification.ContentHash
//     (the scanner-side hash, sourced from media_assets.metadata_json.$
//     .content_hash via SQLiteAssetStore.ListAssetsForReconcile).
//     It feeds BOTH the PR-11 outbox dedupe key
//     (assetID:targetSchema:contentHash) AND the worker's source_version
//     supersede gate. Empty contentHash is rare but legal for newly-
//     inserted rows that haven't been hashed yet — the canonical
//     envelope builder rejects empty hashes, so Production will observe
//     dispatch errors rather than producing dedupe-broken events.
//   - KindOrphan: outbox.EnqueueDelete(assetID). Deterministic key.
//   - KindLifecycleKeyLegacy: payload.DeletePayloadKeys([collection],
//     ["status"], [pointIDs]). One batch per category.
//   - KindLocatorLegacy: payload.DeletePayloadKeys([collection],
//     ["drive_link", "local_path"], [pointIDs]). One batch per category.
//
// Returns legacyStripTotals so the caller can emit per-key metrics.
// Per-call dispatch error from outbox/payload is appended to repairErrs
// and counted in the report, but does NOT bump Applied — Applied is
// computed from the RepairSummary counters only.
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
			if err := s.outbox.EnqueueReindex(ctx, c.AssetID, c.ContentHash); err != nil {
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
			// KindDuplicate, KindMissingVectors, KindDimensionMismatch:
			// no automated repair. Duplicates need manual operator review
			// (which point is canonical? does the extra point carry stale
			// vectors or a different workspace?). MissingVectors and
			// DimensionMismatches are verifier-side gates — the reconciler
			// scrolls with with_vector=false so it cannot detect these.
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
