// types/types_artifact_ref.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// ArtifactRef is a lightweight reference to a finalised artifact,
// returned by AssetFinalizerTx.FinalizeAsset. It carries the minimum
// information needed for downstream consumers (indexing, workflow
// coordination) to reference the artifact without carrying its full
// payload.
type ArtifactRef struct {
	// ArtifactID is the canonical artifact identifier.
	ArtifactID string `json:"artifact_id"`

	// AssetID is the canonical asset identifier in the asset catalog.
	AssetID string `json:"asset_id"`

	// Kind is the artifact category.
	Kind ArtifactKind `json:"kind"`

	// SourceVersion is the logical source version at finalisation time.
	SourceVersion int64 `json:"source_version"`

	// ContentHash is the SHA-256 digest.
	ContentHash string `json:"content_hash"`

	// Location is the canonical published location.
	Location AssetLocation `json:"location"`
}
