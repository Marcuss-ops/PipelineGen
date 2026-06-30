package outbox

// indexRequestV1 is the canonical envelope Dispatcher emits on
// the asset.index.requested.v1 event type (QDRANT-002 PR4).
//
// Producers MUST send the ingest-time content hash as
// source_version — the worker's supersede gate compares it against
// the current media_assets.metadata_json.$.content_hash and
// short-circuits stale events.
type indexRequestV1 struct {
	SchemaVersion      string   `json:"schema_version"`
	EventID            string   `json:"event_id"`
	AssetID            string   `json:"asset_id"`
	Operation          string   `json:"operation"`
	SourceVersion      string   `json:"source_version"`
	TargetIndexVersion string   `json:"target_index_version"`
	RequestedVectors   []string `json:"requested_vectors"`
	RequestedAt        string   `json:"requested_at"`
	EmbeddingModel     string   `json:"embedding_model,omitempty"`
	EmbeddingVersion   string   `json:"embedding_version,omitempty"`
	IdempotencyKey     string   `json:"idempotency_key"`
}
