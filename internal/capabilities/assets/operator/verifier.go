package assets

import "context"

// IndexVerifier is the application-layer port for a live Qdrant
// verification of a single asset's indexed point.
//
// It is intentionally narrow: the operator console calls Verify only
// from the asset detail view or explicit "Verify Qdrant" action, so
// the port exposes a single method that returns the facts the UI
// needs to display.
type IndexVerifier interface {
	// Verify checks whether a point for the given asset exists in the
	// supplied Qdrant collection. If the dependency is unavailable the
	// implementation should return an error so the transport layer can
	// fail closed.
	Verify(ctx context.Context, assetID, collection string) (QdrantPointInfo, error)
}

// QdrantPointInfo is the live verification result for one asset.
type QdrantPointInfo struct {
	// Checked is true when a live request reached Qdrant (regardless of
	// whether the point was found).
	Checked bool `json:"checked"`

	// Present is true when the point exists in the collection.
	Present bool `json:"present"`

	// Collection is the Qdrant collection that was queried.
	Collection string `json:"collection,omitempty"`

	// VectorDimensions is the dimensionality of the stored vector. A
	// value of 0 means the implementation did not retrieve the vector.
	VectorDimensions int `json:"vector_dimensions"`

	// PayloadLifecycleState mirrors the lifecycle_state payload field
	// if present.
	PayloadLifecycleState string `json:"payload_lifecycle_state,omitempty"`

	// PayloadAssetID is the asset_id payload value when present.
	PayloadAssetID string `json:"payload_asset_id,omitempty"`
}
