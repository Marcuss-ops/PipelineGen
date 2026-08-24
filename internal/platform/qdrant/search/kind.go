// Package qdrant — KindAsset is the discriminant that selects
// per-path behavior (workspace validation + filter compile +
// MinScore/limit defaults + wire-shape conversion) for the
// unified SemanticAssetSearchAdapter shipped by
// PR-POSTPROCESSOR-UNIFICATION-PHASE-4 (August 2026).
//
// godlike/06 SSOT (one canonical owner per fact): KindAsset is the
// SOLE canonical owner of the clip vs stock route discriminator.
// Adding a third kind (e.g. voiceover) is a godlike/06 one-canonical-
// owner-per-fact expansion -- requires a 2nd sentinel, a 2nd convert
// helper, and a 2nd if-branch in SearchAssets. Do not add a "lookup
// by string" helper because that breaks compile-time pin discipline.
//
// The 7-day backward-compat window (per PR-POSTPROCESSOR-UNIFICATION-
// PHASE-4 user spec) is achieved by keeping the legacy constructors
// NewClipSearchAdapter + NewStockSearchAdapter as thin wrappers
// around the canonical NewSemanticAssetSearchAdapter constructor;
// forward-pointer PR-CLIPS-STOCK-PORT-RETIRE converts the 2 narrow
// ports to true Go aliases (type X = AssetSearchPort) at soak-end.
package search

// KindAsset discriminates the per-path behavior of
// SemanticAssetSearchAdapter.SearchAssets. Adding a new value is a
// godlike/06 SSOT expansion (one canonical owner per fact) -- see
// godoc above.
type KindAsset int

const (
	// KindClip: workspace-validated + filter-compiled + 5-field
	// wire-shape (drive_link="" per QDRANT-001). MinScore=0.5,
	// Limit=20.
	KindClip KindAsset = iota

	// KindStock: NO workspace guard (admin/reconcile path only) +
	// raw map filter (source=stock + lifecycle_state=ACTIVE) +
	// 5-field wire-shape with drive_link populated from payload.
	// MinScore=0.3, Limit=5.
	KindStock
)

// String returns the canonical wire-name for a KindAsset value. Used
// by typed-error envelopes + log fields so operators see the
// discriminant, never the integer.
func (k KindAsset) String() string {
	switch k {
	case KindClip:
		return "clip"
	case KindStock:
		return "stock"
	default:
		return "unknown"
	}
}
