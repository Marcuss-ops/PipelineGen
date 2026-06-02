// Package vectorstore provides a canonical interface and Qdrant implementation
// for vector search over media assets. This is the single point of integration
// for ANN (approximate nearest neighbor) and hybrid (dense + sparse) search in PipelineGen.
//
// Architecture:
//   Store interface → QdrantClient → Qdrant REST API
//   SQLite remains the canonical metadata store; Qdrant is the real-time index.
//
// Named vectors:
//   "text"       — 768d  from intfloat/multilingual-e5-base
//   "visual"     — 512d  from clip-ViT-B-32 (SentenceTransformer CLIP)
//   "bm25_text"  — sparse vector (indices + values) from client-side BM25 tokenization
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

	// SearchText is the rich search text for FTS and CrossEncoder reranking (768d from multilingual-e5-base)
	SearchText string `json:"search_text,omitempty"`

	// TextEmbedding is the 768d text embedding vector (intfloat/multilingual-e5-base)
	TextEmbedding []float32 `json:"-"`

	// VisualEmbedding is the 512d visual embedding vector (clip-ViT-B-32)
	VisualEmbedding []float32 `json:"-"`

	// AudioEmbedding is the 512d audio embedding vector (CLAP)
	AudioEmbedding []float32 `json:"-"`

	// Tags are searchable metadata tags
	Tags []string `json:"tags,omitempty"`

	// CreatedAt is the asset creation timestamp
	CreatedAt time.Time `json:"created_at,omitempty"`

	// SparseBM25 holds the sparse vector (indices + values) for BM25 search.
	// Generated client-side from SearchText during upsert.
	SparseBM25 *SparseVector `json:"-"`
}

// SparseVector represents a sparse embedding with explicit indices and values.
// Used for BM25 / SPLADE-based sparse search in Qdrant.
// Indices must be sorted in ascending order (Qdrant requirement).
type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
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
// Fields populated from Qdrant payload. SearchText and Tags enable rich CrossEncoder reranking.
type SearchResult struct {
	AssetID    string   `json:"asset_id"`
	Score      float64  `json:"score"`
	Source     string   `json:"source"`
	Name       string   `json:"name"`
	LocalPath  string   `json:"local_path,omitempty"`
	DriveLink  string   `json:"drive_link,omitempty"`
	Category   string   `json:"category,omitempty"`
	MediaType  string   `json:"media_type,omitempty"`
	Style      string   `json:"style,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	SearchText string   `json:"search_text,omitempty"`
}

// HybridSearchRequest combines dense and sparse vectors for Qdrant hybrid search.
// Uses prefetch + RRF (Reciprocal Rank Fusion) to merge results.
type HybridSearchRequest struct {
	// QueryText is the raw text for BM25 sparse tokenization.
	QueryText string

	// DenseVector is the query dense embedding (e.g., E5 text query vector).
	DenseVector []float32

	// DenseVectorName is the named dense vector to search (default: "text").
	DenseVectorName string

	// SparseVector is the BM25-tokenized sparse query vector.
	SparseVector *SparseVector

	// SparseVectorName is the named sparse vector to search (default: "bm25_text").
	SparseVectorName string

	// Limit is the final number of results after RRF fusion.
	Limit int

	// MinScore filters results below this threshold.
	MinScore float64

	// Source optionally filters by source system.
	Source string

	// Category optionally filters by asset category.
	Category string

	// MediaType optionally filters by media type.
	MediaType string
}

// CollectionInfo holds metadata about a Qdrant collection for monitoring.
type CollectionInfo struct {
	PointsCount int64 `json:"points_count"`
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

	// UpsertAssets indexes multiple assets in a single batch operation.
	// Significantly faster than N individual UpsertAsset calls — use for backfill/import.
	UpsertAssets(ctx context.Context, assets []VectorAsset) error

	// Search performs an ANN search using the given query vector.
	// Returns results sorted by descending cosine similarity.
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)

	// DeleteAsset removes an asset from the vector store by ID.
	DeleteAsset(ctx context.Context, assetID string) error

	// Health checks if the vector store is reachable and responsive.
	Health(ctx context.Context) error

	// CollectionInfo returns metadata about the collection (size, status).
	CollectionInfo(ctx context.Context) (*CollectionInfo, error)

	// Close releases any resources held by the client.
	Close() error

	// HybridSearch performs a hybrid dense+sparse search using Qdrant prefetch + RRF fusion.
	// Combines dense ANN similarity with BM25 lexical matching.
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error)
}