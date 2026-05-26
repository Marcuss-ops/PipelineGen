// Package vectorstore provides a canonical interface and Qdrant implementation
// for vector search over media assets. This is the single point of integration
// for ANN (approximate nearest neighbor) search in PipelineGen.
//
// Architecture:
//   Store interface → QdrantClient → Qdrant REST API
//   SQLite remains the canonical metadata store; Qdrant is the real-time index.
//
// Named vectors:
//   "text"   — 384d  from all-MiniLM-L6-v2 (SentenceTransformer)
//   "visual" — 512d  from clip-ViT-B-32 (SentenceTransformer CLIP)
package vectorstore

import (
	"context"
	"time"
)

// VectorAsset is the data structure stored in Qdrant as a point payload.
// SQLite remains the canonical store; Qdrant holds a search-optimised subset.
type VectorAsset struct {
	// AssetID is the unique identifier (e.g. "artlist_123", "clip_456")
	AssetID string `json:"asset_id"`

	// Source is the origin system: "artlist", "youtube", "stock", "image", "voiceover"
	Source string `json:"source"`

	// Name is the human-readable asset title
	Name string `json:"name"`

	// LocalPath is the filesystem path to the asset
	LocalPath string `json:"local_path,omitempty"`

	// DriveLink is the Google Drive URL (if uploaded)
	DriveLink string `json:"drive_link,omitempty"`

	// Category is the asset category (e.g. "animals", "nature", "cinematic")
	Category string `json:"category,omitempty"`

	// Style is the visual style (e.g. "cinematic", "abstract", "realistic")
	Style string `json:"style,omitempty"`

	// MediaType indicates the type: "video", "image", "audio"
	MediaType string `json:"media_type,omitempty"`

	// DurationMs is the clip duration in milliseconds (0 for images)
	DurationMs int `json:"duration_ms,omitempty"`

	// TextEmbedding is the 384d text embedding vector (all-MiniLM-L6-v2)
	TextEmbedding []float32 `json:"-"`

	// VisualEmbedding is the 512d visual embedding vector (clip-ViT-B-32)
	VisualEmbedding []float32 `json:"-"`

	// Tags are searchable metadata tags
	Tags []string `json:"tags,omitempty"`

	// CreatedAt is the asset creation timestamp
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// SearchRequest parameters for vector search.
type SearchRequest struct {
	// QueryVector is the dense embedding vector for the query.
	// Must match the dimensionality of VectorName.
	QueryVector []float32

	// VectorName identifies which named vector to search: "text" or "visual".
	VectorName string

	// Limit is the maximum number of results to return.
	Limit int

	// MinScore filters results below this cosine similarity threshold.
	MinScore float64

	// Source optionally filters by source system.
	Source string `json:"source,omitempty"`

	// Category optionally filters by asset category.
	Category string `json:"category,omitempty"`

	// MediaType optionally filters by media type.
	MediaType string `json:"media_type,omitempty"`
}

// SearchResult is a single match from a vector search.
type SearchResult struct {
	AssetID   string  `json:"asset_id"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
	Name      string  `json:"name"`
	LocalPath string  `json:"local_path,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
	Category  string  `json:"category,omitempty"`
	MediaType string  `json:"media_type,omitempty"`
}

// Store is the canonical interface for vector-based asset search.
// Implementations: QdrantClient (HTTP), and future Milvus, Pinecone, etc.
type Store interface {
	// EnsureCollection creates the collection with the correct named vector config
	// if it does not already exist. Idempotent.
	EnsureCollection(ctx context.Context) error

	// UpsertAsset indexes a single asset into the vector store.
	// Creates or replaces the point. Idempotent.
	UpsertAsset(ctx context.Context, asset VectorAsset) error

	// Search performs an ANN search using the given query vector.
	// Returns results sorted by descending cosine similarity.
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)

	// DeleteAsset removes an asset from the vector store by ID.
	DeleteAsset(ctx context.Context, assetID string) error

	// Health checks if the vector store is reachable and responsive.
	Health(ctx context.Context) error

	// Close releases any resources held by the client.
	Close() error
}