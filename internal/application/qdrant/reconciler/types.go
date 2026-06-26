// Package reconciler implements QDRANT-005B: the dual-store reconciler
// that compares media_assets (SQLite) against Qdrant points via
// payload.asset_id and dispatches canonical repairs via the outbox.
//
// Scope (from docs/architecture/qdrant/QDRANT-005.md):
//   - Compare real ID sets, NOT counts.
//   - Classify into the 9 categories below.
//   - Repair routes: missing/stale -> outbox EnqueueAndIndex;
//     orphan -> outbox EnqueueAndDelete; locator_legacy /
//     lifecycle_key_legacy -> qdrant.Client.DeletePayloadKeys
//     (canonical for legacy key stripping per client.go docstring;
//     no outbox primitive exists for partial payload mutation).
//   - Idempotent; dry-run by default.
//
// Invariant: the reconciler NEVER calls outbox.Dispatcher
// concurrently for the same asset_id; the outbox ON CONFLICT(event_key)
// DO NOTHING guarantee collapses duplicates.
package reconciler

import "time"

// ClassificationKind enumerates the 9 categories a paired (asset_id,
// qdrantPoint) can fall into. Priority order (highest first; used by
// classifyPair when multiple conditions apply):
//
//	1. Missing              — in SQLite, not in Qdrant
//	2. Orphan               — in Qdrant, not in SQLite
//	3. NonCanonicalPointID  — Qdrant point ID does NOT match the
//	                           AssetIDToQdrantPointID(asset_id) contract
//	4. PayloadIncomplete    — missing required payload key
//	5. VersionStale         — any channel embedding_version_<ch>
//	                           mismatches the manifest ModelVersion
//	6. LifecycleMismatch    — sqlite lifecycle_state != payload
//	7. WorkspaceMismatch    — sqlite workspace_id != payload
//	8. LifecycleKeyLegacy   — payload uses retired "status" key
//	   (instead of canonical "lifecycle_state"; QDRANT-004 SSOT)
//	9. LocatorLegacy        — payload carries "drive_link" /
//	                           "local_path" (retired by QDRANT-001)
type ClassificationKind string

const (
	KindMissing             ClassificationKind = "missing"
	KindOrphan              ClassificationKind = "orphan"
	KindNonCanonicalPointID ClassificationKind = "non_canonical_point_id"
	KindPayloadIncomplete   ClassificationKind = "payload_incomplete"
	KindVersionStale        ClassificationKind = "version_stale"
	KindLifecycleMismatch   ClassificationKind = "lifecycle_mismatch"
	KindWorkspaceMismatch   ClassificationKind = "workspace_mismatch"
	KindLifecycleKeyLegacy  ClassificationKind = "lifecycle_key_legacy"
	KindLocatorLegacy       ClassificationKind = "locator_legacy"
)

// ReconcileOptions is the input for Service.Reconcile.
type ReconcileOptions struct {
	// DryRun, when true (default in admin command), produces a report
	// without dispatching any repair action. When false, repairs run
	// through the canonical outbox + DeletePayloadKeys.
	DryRun bool

	// BatchSize controls how many points are scrolled per page
	// (default 500). Round-trips to Qdrant are the dominant latency
	// cost; a larger batch reduces cursor overhead at the cost of
	// memory.
	BatchSize int

	// Collection is the Qdrant collection to reconcile. Required.
	Collection string

	// ReportPath, if non-empty, causes the JSON report to be written
	// to this file path before the function returns.
	ReportPath string

	// IncludeLifecycleStates, when non-empty, restricts the SQLite
	// scan to assets in these lifecycle states (e.g. ["ACTIVE",
	// "STAGING"]). Empty means "no restriction" (compare every asset,
	// including DELETED — useful for verifying that DELETED Qdrant
	// points were cleaned up via DeleteEnqueued).
	IncludeLifecycleStates []string

	// Now overrides time.Now for deterministic tests. Defaults to
	// time.Now.
	Now func() time.Time
}

// Classification is a single issue found by the reconciler for an
// asset_id.
//
// AssetID is the canonical media_assets.id (SQLite side). QdrantPointID
// is the Qdrant point ID observed during the scan; empty when the
// asset is missing entirely from Qdrant. Channel applies only when
// Kind == KindVersionStale and identifies the embedding channel whose
// payload value mismatched the schema.
//
// LocatorKeys applies only when Kind == KindLocatorLegacy and lists
// the retired locator keys observed in this particular point's payload
// (subset of {"drive_link", "local_path"}). The scanner populates
// LocatorKeys with EXACTLY the keys present in the payload —
// service-layer metric accounting uses LocatorKeys to bump the
// canonical payload_legacy_cleaned_total{legacy_key=...} series so
// the counter reflects "keys actually removed" rather than "points
// touched".
type Classification struct {
	Kind          ClassificationKind `json:"kind"`
	AssetID       string              `json:"asset_id"`
	QdrantPointID string              `json:"qdrant_point_id,omitempty"`
	Channel       string              `json:"channel,omitempty"`
	Details       string              `json:"details"`
	LocatorKeys   []string            `json:"locator_keys,omitempty"`
}

// ReconcileReport is the machine-readable output of a reconcile run.
// The JSON form is consumed by ops dashboards / alert routing.
type ReconcileReport struct {
	StartedAt     string                 `json:"started_at"`
	CompletedAt   string                 `json:"completed_at"`
	DurationMs    int64                  `json:"duration_ms"`
	DryRun        bool                   `json:"dry_run"`
	Collection    string                 `json:"collection"`
	SchemaVersion string                 `json:"schema_version"`
	Applied       bool                   `json:"applied"`

	// Counts mirrors len(Classifications[*]); the map is the
	// machine-friendly rollup and is authoritative even when the
	// Classifications list is truncated.
	Counts map[ClassificationKind]int `json:"counts"`

	// Classifications is the full list, capped at MaxClassifications.
	// Inspect Truncated + DisplayedCount to know whether the list is
	// complete; Counts is always authoritative.
	Classifications []Classification `json:"classifications,omitempty"`

	// Truncated is true when the Classifications list was capped at
	// MaxClassifications. Operators reading the JSON MUST check this
	// flag before assuming the list is complete.
	Truncated bool `json:"truncated"`

	// DisplayedCount is len(Classifications); equals len(pairs) when
	// not truncated, MaxClassifications when truncated.
	DisplayedCount int `json:"displayed_count"`

	// RepairSummary records per-action dispatch counts. Zeros when
	// DryRun.
	RepairSummary RepairSummary `json:"repair_summary"`

	// ScannedTotals records the scan coverage (so an operator can
	// tell "no missing rows" from "scan was truncated").
	ScannedTotals ScannedTotals `json:"scanned_totals"`

	// Errors contains non-fatal diagnostics that did not stop the
	// run (e.g. one scrolled page failed).
	Errors []string `json:"errors,omitempty"`
}

// ScannedTotals records scan coverage.
type ScannedTotals struct {
	SQLiteAssets int `json:"sqlite_assets"`
	QdrantPoints int `json:"qdrant_points"`
	Pairs        int `json:"pairs"`
	// ScrollTruncated is true when the scroll iteration safety cap was
	// hit (maxPages=400 inside service.scrollAll). When true,
	// Classification results may under-count Missing (sqlite rows
	// whose Qdrant counterpart lives past the safety cap) and
	// Counts should NOT be trusted operationally — alerts SHOULD be
	// ignored until a clean re-run is performed.
	ScrollTruncated bool `json:"scroll_truncated"`
}

// RepairSummary records the repair dispatch counts.
type RepairSummary struct {
	ReindexEnqueued int `json:"reindex_enqueued"`
	DeleteEnqueued  int `json:"delete_enqueued"`
	PayloadStrips   int `json:"payload_strips"`
}

// SchemaVersions pairs the canonical schema with the per-channel
// model-version map used by VersionStale detection.
//
// Per-channel comparison (NOT against SQLite asset.index_version):
// the canonical write path (payload_mapper.BuildPayload) stamps
// `embedding_version_<channel> = EmbeddingSpec.ModelVersion` on every
// point. The reconciler compares that stamp against SchemaVersions
// to detect drift. SQLite index_version is informational only and
// not consulted here.
//
// RequiredKeys is the payload minimum (asset_id, name, source,
// lifecycle_state). Missing any = PayloadIncomplete.
type SchemaVersions struct {
	Version           string
	PhysicalName      string
	RuntimeAlias      string
	PerChannelVersion map[string]string // channel -> ModelVersion
	RequiredKeys      []string
}

// MaxClassifications caps the list output to keep dashboard JSON
// bounded on large drifts (100k+ orphans). The Counts map remains
// authoritative.
const MaxClassifications = 10000

// AllClassificationKinds is the canonical, deterministic enumeration of
// every ClassificationKind value, in priority order top-to-bottom.
// Used by dashboards and the cmd/admin reconcile command to render all
// 9 categories (including zero-count entries) so operators see exactly
// which categories the scan covered.
var AllClassificationKinds = []ClassificationKind{
	KindMissing,
	KindOrphan,
	KindNonCanonicalPointID,
	KindPayloadIncomplete,
	KindVersionStale,
	KindLifecycleMismatch,
	KindWorkspaceMismatch,
	KindLifecycleKeyLegacy,
	KindLocatorLegacy,
}
