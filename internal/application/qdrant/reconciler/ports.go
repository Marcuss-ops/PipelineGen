package reconciler

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
	Payload map[string]interface{}
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
// EnqueueReindex emits one asset.index.requested.v1 outbox event per
// call. Implementations are responsible for any event_key shaping
// (e.g. uuid-suffixed keys so consecutive reconcile --apply runs
// produce distinct events). EnqueueDelete emits one
// asset.index.delete_requested.v1 outbox event per call.
//
// Idempotency: consecutive calls produce distinct outbox rows so the
// worker fires each repair; the worker's supersede gate (in
// IndexingHandler) collapses repeating fix-up jobs when the
// metadata content_hash is unchanged between calls.
type OutboxRepairEnqueuer interface {
	EnqueueReindex(ctx context.Context, assetID string) error
	EnqueueDelete(ctx context.Context, assetID string) error
}

// QdrantPayloadMutator strips legacy payload keys from existing
// points. This is the ONLY QDRANT-005B scope where the reconciler
// touches Qdrant directly: legacy locator / lifecycle-key cleanup
// has no outbox primitive (partial-payload mutation is not modelled
// in outbox_events), and qdrant.Client.DeletePayloadKeys is documented
// as the canonical mechanism for this exact use case (see
// internal/infrastructure/qdrant/client.go::DeletePayloadKeys
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
var writeJSONFile = func(path string, v interface{}) error {
	return writeFileDefault(path, v)
}
