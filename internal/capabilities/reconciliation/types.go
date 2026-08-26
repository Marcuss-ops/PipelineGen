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
package reconciliation

import (
)

import "time"

// ClassificationKind enumerates the 12 categories a paired (asset_id,
// qdrantPoint) can fall into. Priority order (highest first; used by
// classifyPair when multiple conditions apply):
//
//  1. Missing              — in SQLite, not in Qdrant
//  2. Orphan               — in Qdrant, not in SQLite
//  3. Duplicate            — same asset_id appears on multiple Qdrant
//     points (detected during scroll; the first occurrence is kept,
//     subsequent occurrences are flagged)
//  4. NonCanonicalPointID  — Qdrant point ID does NOT match the
//     AssetIDToQdrantPointID(asset_id) contract
//  5. PayloadIncomplete    — missing required payload key
//  6. VersionStale         — any channel embedding_version_<ch>
//     mismatches the manifest ModelVersion
//  7. MissingVectors       — payload present but point has zero or
//     empty vector channels (detected via verifier; reconciler scrolls
//     with with_vector=false so this is a placeholder gate)
//  8. DimensionMismatch    — vector dimension mismatch vs schema
//     (detected via verifier; reconciler scrolls with with_vector=false
//     so this is a placeholder gate)
//  9. LifecycleMismatch    — sqlite lifecycle_state != payload
//  10. WorkspaceMismatch   — sqlite workspace_id != payload
//  11. LifecycleKeyLegacy  — payload uses retired "status" key
//     (instead of canonical "lifecycle_state"; QDRANT-004 SSOT)
//  12. LocatorLegacy       — payload carries "drive_link" /
//     "local_path" (retired by QDRANT-001)
type ClassificationKind string

const (
	KindMissing             ClassificationKind = "missing"
	KindOrphan              ClassificationKind = "orphan"
	KindDuplicate           ClassificationKind = "duplicate"
	KindNonCanonicalPointID ClassificationKind = "non_canonical_point_id"
	KindPayloadIncomplete   ClassificationKind = "payload_incomplete"
	KindVersionStale        ClassificationKind = "version_stale"
	KindMissingVectors      ClassificationKind = "missing_vectors"
	KindDimensionMismatch   ClassificationKind = "dimension_mismatch"
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
// the counter reflects "keys actually removed" rather than "points// touched".
type Classification struct {
	Kind    ClassificationKind `json:"kind"`
	AssetID string             `json:"asset_id"`
	// ContentHash is the media_assets content_hash (extracted from
	// metadata_json.$.content_hash by ListAssetsForReconcile) at
	// scan time. PR 10+11: this hash is the deterministic key
	// component for the outbox event_key, the supersede-gate
	// fingerprint, and the operator audit trail. Empty for Orphan
	// (no SQLite row holds the hash) and for missing-asset_id entries
	// (no qdrant row to point at).
	ContentHash   string   `json:"content_hash,omitempty"`
	QdrantPointID string   `json:"qdrant_point_id,omitempty"`
	Channel       string   `json:"channel,omitempty"`
	Details       string   `json:"details"`
	LocatorKeys   []string `json:"locator_keys,omitempty"`
}

// CategoryGroup is a named aggregation of classifications for a single
// reconciliation category. The Count is always authoritative; Items is
// the (possibly truncated) list of classifications in this category.
type CategoryGroup struct {
	Count int              `json:"count"`
	Items []Classification `json:"items,omitempty"`
}

// ReconciliationReport is the typed, operator-facing report derived from
// a ReconcileReport. It surfaces named, domain-specific categories —
// Missing, Orphans, InvalidPayloads, NonCanonicalIDs, MissingVectors,
// DimensionMismatches, Duplicates — each backed by the underlying
// ClassificationKind enumeration and its detection logic.
//
// The 5 remaining classification kinds (VersionStale, LifecycleMismatch,
// WorkspaceMismatch, LifecycleKeyLegacy, LocatorLegacy) are NOT surfaced
// as named groups here — they are diagnostics that already appear in the
// Counts map and Classifications list; the named groups are the
// action/dispatch categories.
//
// Task 6 (July 2026): this type provides a stable API surface for
// dashboards and alerting rules that need named fields rather than
// iterating a flat []Classification list. The converter method
// ReconcileReport.ToReconciliationReport() maps from the canonical
// kind-based representation into these named groups.
type ReconciliationReport struct {
	Missing             CategoryGroup `json:"missing"`
	Orphans             CategoryGroup `json:"orphans"`
	InvalidPayloads     CategoryGroup `json:"invalid_payloads"`
	NonCanonicalIDs     CategoryGroup `json:"non_canonical_ids"`
	MissingVectors      CategoryGroup `json:"missing_vectors"`
	DimensionMismatches CategoryGroup `json:"dimension_mismatches"`
	Duplicates          CategoryGroup `json:"duplicates"`
}

// ReconcileReport is the machine-readable output of a reconcile run.
// The JSON form is consumed by ops dashboards / alert routing.
type ReconcileReport struct {
	StartedAt     string `json:"started_at"`
	CompletedAt   string `json:"completed_at"`
	DurationMs    int64  `json:"duration_ms"`
	DryRun        bool   `json:"dry_run"`
	Collection    string `json:"collection"`
	SchemaVersion string `json:"schema_version"`
	Applied       bool   `json:"applied"`

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

	// Reconciliation is the typed report derived from Classifications.
	// Populated by Service.Reconcile after classification completes.
	// Dashboard consumers can read this directly instead of iterating
	// the flat Classifications list.
	Reconciliation ReconciliationReport `json:"reconciliation"`

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
//
// PR 10 (June 2026) — fail-closed gates: complete_scan is the single
// boolean that tells operators whether the scan produced actionable
// data. When false, classifications + counts are unreliable and apply
// MUST be aborted (Service.Reconcile returns a non-nil error AND sets
// report.Applied=false in that case). The individual scroll_* flags
// decompose the failure cause; complete_scan is the rolled-up gate.
type ScannedTotals struct {
	SQLiteAssets int `json:"sqlite_assets"`
	QdrantPoints int `json:"qdrant_points"`
	Pairs        int `json:"pairs"`
	// ScrollTruncated is true when the scroll iteration safety cap was
	// hit (maxPages=400 inside service.scrollAll). Set by scrollAll as
	// a hard gate failure (PR 10).
	ScrollTruncated bool `json:"scroll_truncated"`
	// TrailingNextOffset is true when the scroll loop exhausted the
	// batched pages but NextOffset was still non-empty (Qdrant indicated
	// more pages but the cursor advance was cut). Set by scrollAll as
	// a hard gate failure (PR 10).
	TrailingNextOffset bool `json:"trailing_next_offset"`
	// PointsMissingAssetID is the count of Qdrant points scrolled
	// whose payload asset_id was empty/missing. Non-zero blocks apply
	// when SQLite expected > 0 (PR 10).
	PointsMissingAssetID int `json:"points_missing_asset_id"`
	// ScrollPageErrors is the count of scroll pages that returned an
	// error. Any non-zero value blocks apply (PR 10).
	ScrollPageErrors int `json:"scroll_page_errors"`
	// CompleteScan is the rolled-up gate: true ONLY when every
	// PR-10 fail-closed gate passed (no scroll page errors, no cap
	// hit, trailing NextOffset empty, zero missing-asset_id points
	// when SQLite expected > 0, zero QdrantPoints when SQLite
	// expected > 0). Operators SHOULD ignore counts / classifications
	// when false and rerun reconcile.
	CompleteScan bool `json:"complete_scan"`
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
// 12 categories (including zero-count entries) so operators see exactly
// which categories the scan covered.
var AllClassificationKinds = []ClassificationKind{
	KindMissing,
	KindOrphan,
	KindDuplicate,
	KindNonCanonicalPointID,
	KindPayloadIncomplete,
	KindVersionStale,
	KindMissingVectors,
	KindDimensionMismatch,
	KindLifecycleMismatch,
	KindWorkspaceMismatch,
	KindLifecycleKeyLegacy,
	KindLocatorLegacy,
}

// ToReconciliationReport derives the typed ReconciliationReport from
// the flat Classifications list. Each named category extracts its
// corresponding ClassificationKind entries. The Counts map is the
// authoritative source for counts; the Items slice is capped at the
// same MaxClassifications bound as the parent report.
//
// Verifier-only categories (MissingVectors, DimensionMismatch) are
// populated from Classifications produced by the verifier path — the
// reconciler currently does not populate these (it scrolls with
// with_vector=false), so these fields default to zero counts unless
// a future wave adds vector inspection to the scroll phase.
func (r *ReconcileReport) ToReconciliationReport() ReconciliationReport {
	pairs := r.Classifications
	return ReconciliationReport{
		Missing:             extractCategory(pairs, KindMissing, MaxClassifications),
		Orphans:             extractCategory(pairs, KindOrphan, MaxClassifications),
		InvalidPayloads:     extractCategory(pairs, KindPayloadIncomplete, MaxClassifications),
		NonCanonicalIDs:     extractCategory(pairs, KindNonCanonicalPointID, MaxClassifications),
		MissingVectors:      extractCategory(pairs, KindMissingVectors, MaxClassifications),
		DimensionMismatches: extractCategory(pairs, KindDimensionMismatch, MaxClassifications),
		Duplicates:          extractCategory(pairs, KindDuplicate, MaxClassifications),
	}
}

// extractCategory filters a classification list for a single kind and
// returns a CategoryGroup with the count and (possibly truncated) items.
func extractCategory(pairs []Classification, kind ClassificationKind, max int) CategoryGroup {
	items := make([]Classification, 0)
	for _, c := range pairs {
		if c.Kind == kind {
			if len(items) < max {
				items = append(items, c)
			}
		}
	}
	total := 0
	for _, c := range pairs {
		if c.Kind == kind {
			total++
		}
	}
	return CategoryGroup{
		Count: total,
		Items: items,
	}
}
