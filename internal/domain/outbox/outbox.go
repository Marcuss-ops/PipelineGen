// Package outbox defines canonical domain types for the transactional
// outbox pattern used for atomic asset upsert + vector indexing.
package outbox

// Event types for outbox events.
const (
	EventAssetIndexRequested = "asset.index.requested"
)

// IndexRequestPayload is the JSON payload for asset.index.requested events.
type IndexRequestPayload struct {
	AssetID           string `json:"asset_id"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingVersion  string `json:"embedding_version"`
	CollectionVersion string `json:"collection_version"`
}
