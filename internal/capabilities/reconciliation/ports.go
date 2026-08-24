package reconciliation

import "context"

// AssetSnapshot is the minimal SQLite-side projection needed by the
// reconciler. Intentionally narrower than asset.Asset — only the fields
// the scanner actually compares. The wire-up in cmd/admin reads this
// from a JOIN over media_assets (no extra columns beyond what's
// strictly needed).
type AssetSnapshot struct {
	ID             string
	WorkspaceID    string
	LifecycleState string
	ContentHash    string
}

// Points is the page returned by QdrantLister.ScrollPoints.
// Mirrors the shape of qdrant.ScrollResult without leaking the
// qdrant package into the application layer.
type Points struct {
	Items      []PointSnapshot
	NextOffset string
}

// PointSnapshot is the per-point shape needed by the scanner.
type PointSnapshot struct {
	ID      string
	Payload map[string]any
}

// QdrantLister is the Qdrant-side read port. Implementations scroll a
// collection paginated by offset.
type QdrantLister interface {
	ScrollPoints(ctx context.Context, collection string, offset string, limit int) (Points, error)
}

// SQLiteReconcileReader is the SQLite-side read port. Returns the
// assets the reconciler should consider, optionally restricted by
// includeLifecycleStates when non-empty (e.g. ["ACTIVE", "STAGING"]).
type SQLiteReconcileReader interface {
	ListForReconcile(ctx context.Context, includeLifecycleStates []string) ([]AssetSnapshot, error)
}

// AssetPointIDFunc maps asset_id (SQLite) to the canonical Qdrant
// point ID. Provided by the wire-up so the scanner does NOT import
// the qdrant package's AssetIDToQdrantPointID directly — the
// application layer imports infrastructure only via declared ports.
type AssetPointIDFunc func(assetID string) string

// OutboxRepairEnqueuer is the dispatch port for reindex/delete
// repairs. Implementations MUST go through the canonical outbox
// (outbox_events table) — the reconciler MUST NOT mutate Qdrant
// directly via this port.
//
// EnqueueReindex emits ONE asset.index.requested.v1 outbox event
// per call. Implementations are responsible for event_key shaping:
// a DETERMINISTIC key (assetID, targetSchema, contentHash) so two
// reconcile --apply runs with no SQLite change collapse to a single
// outbox row via ON CONFLICT DO NOTHING. PR 11 unified the format
// across the dispatch (ingest) path and the reconcile-repair path;
// before PR 11 each --apply produced a fresh UUID-suffixed event
// which made the outbox layer non-idempotent at the dedupe layer.
//
// contentHash MUST match media_assets.metadata_json.$.content_hash
// at scan time (see SQLiteAssetStore.ListAssetsForReconcile). Empty
// contentHash is rejected by the canonical envelope builder because
// the worker supersede gate (IndexingHandler) requires it.
//
// EnqueueReindex carries the `force` flag (Card 7.1, July 2026):
// when true, the adapter routes through the canonical force
// envelope variant (outboxevents.BuildReindexEnvelopeV1Force) so
// the worker uses the source_version supersede exception. The
// canonical admin reindex path passes force=true. Production
// reconciler --apply also passes force=true today (the operator's
// --apply IS the admin opt-in).
//
// EnqueueDelete emits ONE asset.index.delete_requested.v1 outbox
// event per call; the deterministic delete key ("delete:<assetID>")
// was already correct pre-PR-11.
type OutboxRepairEnqueuer interface {
	EnqueueReindex(ctx context.Context, assetID, contentHash string, force bool) error
	EnqueueDelete(ctx context.Context, assetID string) error
}

// QdrantPayloadMutator strips legacy payload keys from existing
// points. This is the ONLY QDRANT-005B scope where the reconciler
// touches Qdrant directly: legacy locator / lifecycle-key cleanup
// has no outbox primitive (partial-payload mutation is not modelled
// in outbox_events), and qdrant.Client.DeletePayloadKeys is documented
// as the canonical mechanism for this exact use case (see
// internal/platform/qdrant/client.go::DeletePayloadKeys
// docstring). Production wiring adapts qdrant.Client to this port.
//
// Idempotent: repeated calls with the same (collection, keys,
// pointIDs) are no-ops at the Qdrant layer.
type QdrantPayloadMutator interface {
	DeletePayloadKeys(ctx context.Context, collection string, keys []string, pointIDs []string) error
}

// ReportWriter persists the report to disk. Nil writer is valid
// (Service always returns the report in-memory; the caller decides
// where to print / persist).
type ReportWriter interface {
	Write(path string, report *ReconcileReport) error
}

// filesystemReportWriter is the default ReportWriter: writes the
// canonical JSON form to the supplied path.
type filesystemReportWriter struct{}

// Write marshals the report as pretty JSON to path. Errors are
// returned verbatim from json.MarshalIndent and os.WriteFile.
func (filesystemReportWriter) Write(path string, report *ReconcileReport) error {
	if report == nil {
		return nil
	}
	return writeJSONFile(path, report)
}

// writeJSONFile is a tiny helper indirection so internal/scanner_test
// can construct an in-memory equivalent without depending on the
// filesystem.
var writeJSONFile = func(path string, v any) error {
	return writeFileDefault(path, v)
}

// ── Metrics port ─────────────────────────────────────────────────────
//
// The reconciler does NOT import observability/metrics.go directly —
// this indirection lets tests substitute a stubMetrics and keeps
// adapter concrete in internal/platform/qdrant.
//
// Emission contract (per QDRANT-005C):
//   - RecordFindings                : emitted on EVERY run (DryRun + Apply)
//   - RecordVersionMismatchPerChannel: emitted on EVERY run
//   - RecordErrors                  : emitted on EVERY run (counts report.Errors)
//   - RecordRunComplete             : emitted on EVERY run (sets last_success +
//                                     populates duration histogram)
//   - RecordDispatch                : emitted on Apply ONLY (DryRun emits zero
//                                     so dashboards distinguish "scan ran" from
//                                     "repairs ran")
//   - RecordLegacyKeyStripped       : emitted on Apply ONLY
//
// A noopMetrics default is used when Deps.Metrics is nil so test
// callers don't have to plumb a recorder.

type Metrics interface {
	// RecordFindings iterates counts (9 ClassificationKind values)
	// and bumps findings_total{kind=...} for each non-zero entry.
	// Implementations MUST NOT crash on zero-value counts.
	RecordFindings(counts map[ClassificationKind]int)

	// RecordVersionMismatchPerChannel iterates the per-channel count
	// map (channel -> count) and bumps
	// version_mismatch_per_channel_total{channel=...} per entry.
	// Values NOT in the map emit ZERO.
	RecordVersionMismatchPerChannel(perChannel map[string]int)

	// RecordDispatch bumps dispatches_total{action=...} by n. Action
	// label values: "reindex" | "delete" | "payload_strip".
	RecordDispatch(action string, n int)

	// RecordLegacyKeyStripped bumps legacy_cleaned_total{legacy_key=...}
	// by n. legacy_key label values: "status" | "drive_link" | "local_path".
	RecordLegacyKeyStripped(legacyKey string, n int)

	// RecordErrors bumps errors_total by n. n may be 0 (no-op).
	RecordErrors(n int)

	// RecordRunComplete records mode ("dry_run" | "apply") + duration
	// in seconds. Implementations also set last_success_timestamp_seconds
	// to time.Now().Unix() so "no reconcile in N minutes" alerts work.
	RecordRunComplete(mode string, durationSeconds float64)
}

// noopMetrics is the default Metrics implementation when Deps.Metrics
// is nil. Satisfies the interface with empty bodies; tests + dry-run
// callers don't have to construct a recorder.
type noopMetrics struct{}

func (noopMetrics) RecordFindings(map[ClassificationKind]int)      {}
func (noopMetrics) RecordVersionMismatchPerChannel(map[string]int) {}
func (noopMetrics) RecordDispatch(string, int)                     {}
func (noopMetrics) RecordLegacyKeyStripped(string, int)            {}
func (noopMetrics) RecordErrors(int)                               {}
func (noopMetrics) RecordRunComplete(string, float64)              {}
