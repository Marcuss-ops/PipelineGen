// Package qdrant provides a canonical interface and Qdrant implementation
// for vector search over media assets. This is the single point of integration
// for ANN (approximate nearest neighbor) and hybrid (dense + sparse) search in PipelineGen.
//
// Architecture:
//
//	Store interface → QdrantClient → Qdrant REST API
//	SQLite remains the canonical metadata store; Qdrant is the real-time index.
//
// Named vectors:
//
//	"text"       — 768d  from intfloat/multilingual-e5-base — semantic (title+summary+topics+hook)
//	"transcript" — 768d  from intfloat/multilingual-e5-base — full Whisper transcript
//	"visual"     — 512d  from clip-ViT-B-32 (SentenceTransformer CLIP)
//	"audio"      — 512d  from laion/clap-htsat-fused
//	"bm25_text"  — sparse vector (indices + values) from client-side BM25 tokenization
//
// Dual-vector search (for YouTube clips):
//   - semantic vector ("text"):  fast search by general meaning of the clip
//     Generated from search_text = title + summary + topics + hook
//   - transcript vector ("transcript"): precise search within the spoken content
//     Generated from the full Whisper transcription (.txt file)
package qdrant

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

	// Language is the ISO 639-1 language code (e.g. "en", "es", "it"). Populated from YouTube metadata.
	Language string `json:"language,omitempty"`

	// YouTubeVideoID is the YouTube video ID (e.g. "dQw4w9WgXcQ")
	YouTubeVideoID string `json:"youtube_video_id,omitempty"`

	// YouTubeURL is the full YouTube URL reconstructed from video ID
	YouTubeURL string `json:"youtube_url,omitempty"`

	// StartTime is the clip start time in HH:MM:SS format
	StartTime string `json:"start_time,omitempty"`

	// EndTime is the clip end time in HH:MM:SS format
	EndTime string `json:"end_time,omitempty"`

	// TextEmbedding is the 768d text embedding vector (intfloat/multilingual-e5-base)
	TextEmbedding []float32 `json:"-"`

	// VisualEmbedding is the 512d visual embedding vector (clip-ViT-B-32)
	VisualEmbedding []float32 `json:"-"`

	// TranscriptEmbedding is the embedding of the Whisper audio transcript
	TranscriptEmbedding []float32 `json:"-"`

	// AudioEmbedding is the 512d audio embedding vector (CLAP)
	AudioEmbedding []float32 `json:"-"`

	// Tags are searchable metadata tags
	Tags []string `json:"tags,omitempty"`

	// CreatedAt is the asset creation timestamp
	CreatedAt time.Time `json:"created_at,omitempty"`

	// SparseBM25 holds the sparse vector (indices + values) for BM25 search.
	// Generated client-side from SearchText during upsert.
	SparseBM25 *SparseVector `json:"-"`

	// EmbeddingVersion tracks which model/date generated the embedding (#9)
	EmbeddingVersion string `json:"embedding_version,omitempty"`
	// SearchTextVersion tracks which schema version produced the search_text (#9)
	SearchTextVersion string `json:"search_text_version,omitempty"`
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

	// Language optionally filters by ISO 639-1 language code.
	Language string `json:"language,omitempty"`
}

// SearchResult is a single match from a vector search.
// Fields populated from Qdrant payload. SearchText and Tags enable rich CrossEncoder reranking.
type SearchResult struct {
	AssetID        string   `json:"asset_id"`                  // Logical asset ID (e.g. "yt_W6ESLDpD8Ag_874_938")
	QdrantPointID  string   `json:"qdrant_point_id,omitempty"` // Qdrant numeric/UUID point ID (technical)
	Score          float64  `json:"score"`
	Reason         string   `json:"reason,omitempty"` // Human-readable match reason
	Source         string   `json:"source"`
	Name           string   `json:"name"`
	LocalPath      string   `json:"local_path,omitempty"`
	DriveLink      string   `json:"drive_link,omitempty"`
	Category       string   `json:"category,omitempty"`
	MediaType      string   `json:"media_type,omitempty"`
	Style          string   `json:"style,omitempty"`
	Language       string   `json:"language,omitempty"`
	YouTubeVideoID string   `json:"youtube_video_id,omitempty"`
	YouTubeURL     string   `json:"youtube_url,omitempty"`
	StartTime      string   `json:"start_time,omitempty"`
	EndTime        string   `json:"end_time,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	// Versioning fields (#9)
	EmbeddingVersion  string `json:"embedding_version,omitempty"`   // Model + date used to generate embedding
	SearchTextVersion string `json:"search_text_version,omitempty"` // Schema version of search_text content
}

// HybridSearchRequest combines dense and sparse vectors for Qdrant hybrid search.
// Uses prefetch + RRF (Reciprocal Rank Fusion) to merge results from multiple vector spaces.
// Supports up to 3-way fusion: semantic (text) + transcript + BM25 (sparse).
type HybridSearchRequest struct {
	// QueryText is the raw text for BM25 sparse tokenization.
	QueryText string

	// DenseVector is the query dense embedding for semantic search ("text" vector).
	DenseVector []float32

	// DenseVectorName is the named dense vector to search (default: "text").
	DenseVectorName string

	// TranscriptVector is the query dense embedding for transcript search ("transcript" vector).
	// When set, enables dual-vector search: semantic + transcript fused via RRF.
	TranscriptVector []float32

	// TranscriptVectorName is the named transcript vector to search (default: "transcript").
	TranscriptVectorName string

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

	// Language optionally filters by ISO 639-1 language code.
	Language string
}

// ValidationError describes a specific field validation failure from ValidateBeforeIndex.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidateBeforeIndex checks that a VectorAsset has critical fields before upsert.
// Returns nil if valid, or a list of validation errors.
// Only hard-requires AssetID + Source. Other fields are best-effort warnings.
func ValidateBeforeIndex(asset VectorAsset) []ValidationError {
	var errs []ValidationError

	if asset.AssetID == "" {
		errs = append(errs, ValidationError{"asset_id", "missing asset_id"})
	}
	if asset.Source == "" {
		errs = append(errs, ValidationError{"source", "missing source"})
	}

	return errs
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

	// CollectionInfo returns metadata about the physical (versioned)
	// collection. Alias for PhysicalCollectionInfo — older callers keep
	// compiling while the cross-check migrates to OperationCollectionInfo.
	// New code should call OperationCollectionInfo explicitly for
	// production-reality stats, or PhysicalCollectionInfo for backfill
	// visibility.
	CollectionInfo(ctx context.Context) (*CollectionInfo, error)

	// OperationCollectionInfo returns metadata about the collection that
	// data-plane operations (search, upsert, delete) currently target —
	// i.e. the one served by the alias when aliasing is wired, or the
	// logical name when aliasing is disabled. IndexHealth uses this so the
	// cross-check reflects what users actually see, not the versioned
	// collection being backfilled underneath.
	OperationCollectionInfo(ctx context.Context) (*CollectionInfo, error)

	// PhysicalCollectionInfo returns metadata about the versioned physical
	// collection (no alias indirection). Admin endpoints and migration
	// scripts monitor this to track the backfill window. May diverge from
	// OperationCollectionInfo during a SwitchAlias swap.
	PhysicalCollectionInfo(ctx context.Context) (*CollectionInfo, error)

	// Close releases any resources held by the client.
	Close() error

	// HybridSearch performs a hybrid dense+sparse search using Qdrant prefetch + RRF fusion.
	// Combines dense ANN similarity with BM25 lexical matching.
	HybridSearch(ctx context.Context, req HybridSearchRequest) ([]SearchResult, error)

	// IndexHealth returns a cross-check between DB, embeddings, and Qdrant.
	IndexHealth(ctx context.Context) (*IndexHealthReport, error)

	// ListPointIDs returns a sample of asset_ids by scrolling the collection.
	// PR3-5b cross-check: realtime.Service.IndexHealth calls this to diff
	// SQLite indexed IDs against the actual Qdrant payload. limit caps the
	// returned slice; implementations may also enforce a hard upper bound.
	ListPointIDs(ctx context.Context, limit int) ([]string, error)

	// CleanupStalePoints iterates over all points and validates them via the
	// provided validator function. Uses a two-pass tombstone approach:
	// first pass marks stale points, second pass (next run) hard-deletes them.
	// Validator receives (assetID, driveFileID, driveLink) and returns (valid, error).
	// Returns the number of points hard-deleted.
	CleanupStalePoints(ctx context.Context, validator func(assetID, driveFileID, driveLink string) (bool, error)) (int, error)

	// ScrollAssetIDsPage scrolls the entire collection (no hard cap,
	// unlike ListPointIDs which is sampled) and invokes fn per non-empty
	// batch of distinct asset_id strings extracted from point payloads.
	// fn receives each batch and is responsible for accumulating/
	// processing — the Store does not buffer the full result set.
	// Iteration stops on empty page, no next_page_offset, fn error, or
	// ctx cancel. Backed by internal/app/sweepers.go::startQdrantGhostSweeper.
	ScrollAssetIDsPage(ctx context.Context, batchSize int, fn func([]string) error) error

	// DeletePoints batch-deletes Qdrant points whose payload.asset_id
	// matches ANY of the given assetIDs. Internally chunks at a safe
	// Qdrant filter size (default 100). Idempotent.
	DeletePoints(ctx context.Context, assetIDs []string) error
}

// IndexHealthReport provides a cross-check between SQLite and Qdrant.
// Used by the /api/media/index-health endpoint.
//
// PR3-5b fields are the canonical cross-check surface: the seven numbers
// (sqlite_assets, sqlite_indexed, qdrant_points, missing_in_qdrant,
// orphan_in_qdrant, pending_outbox, dead_letter) plus a sample of IDs on
// each side so operators can drill in to the drift. The pre-existing
// DBTotal / WithEmbedding / StaleQdrantIDs fields are preserved for
// back-compat with earlier /api/media/index-health callers but the new
// PR3-5b fields are what monitoring systems should alert on.
type IndexHealthReport struct {
	// Legacy fields — kept for back-compat with the v1 /api/media/index-health payload.
	DBTotal           int64    `json:"db_total"`
	WithEmbedding     int64    `json:"with_embedding"`
	WithoutEmbedding  int64    `json:"without_embedding"`
	QdrantPoints      int64    `json:"qdrant_points"`
	DBToQdrantDelta   int64    `json:"db_to_qdrant_delta"`         // db_total - qdrant_points
	MissingSearchText int64    `json:"missing_search_text"`        // records with empty search_text
	MissingLanguage   int64    `json:"missing_language"`           // records with empty language
	StaleQdrantIDs    []string `json:"stale_qdrant_ids,omitempty"` // Qdrant points with no DB record (sample)

	// PR3-5b canonical cross-check fields.
	SQLiteAssets    int64 `json:"sqlite_assets"`     // total media_assets rows
	SQLiteIndexed   int64 `json:"sqlite_indexed"`    // media_assets rows with non-empty embedding_json
	MissingInQdrant int64 `json:"missing_in_qdrant"` // indexed-in-SQLite but absent in Qdrant (under-count of Qdrant)
	OrphanInQdrant  int64 `json:"orphan_in_qdrant"`  // present in Qdrant but no media_assets row (over-count of Qdrant)
	PendingOutbox   int64 `json:"pending_outbox"`    // media_index_outbox rows in 'pending' state — work the worker hasn't yet processed
	DeadLetter      int64 `json:"dead_letter"`       // media_index_outbox rows in 'dead_letter' state — manually inspect / re-enqueue

	// PR3-5b ID samples (capped; for drill-in, not full diff). MissingInQdrantIDs and OrphanInQdrantIDs help operators locate drift.
	MissingInQdrantIDs []string `json:"missing_in_qdrant_ids,omitempty"` // sample of SQLite-indexed asset_ids absent from sampled Qdrant scroll
	OrphanInQdrantIDs  []string `json:"orphan_in_qdrant_ids,omitempty"`  // sample of Qdrant asset_ids absent from media_assets

	// PR3-5b quality signals (Task 3). Operators should alert on these
	// directly rather than on the absolute count numbers — false here means
	// the "count is zero" conclusions are not trustworthy.
	//
	// QdrantHealthy = the /readyz probe succeeded.
	// ChecksComplete = every independent source (qdrant, sqlite, outbox)
	//   responded without error.
	// Degraded = ok=false AND the failure is operational (Qdrant down or
	//   probe failure), not data drift. A ops-facing colour cue that tells
	//   the on-call "fix Qdrant" vs. "fix ingestion".
	// DegradedSources = granular per-leg breakdown that names the exact
	//   failing check leg when degraded (a transient outbox hiccup then
	//   layers on top of a Qdrant outage can be told apart). One entry per
	//   leg that returned false on this call: "qdrant" (probe failed),
	//   "qdrant_info" (OperationCollectionInfo/ListPointIDs failed),
	//   "sqlite" (any clips.* read failed), "outbox" (any outbox.* read
	//   failed). Nil deps do NOT contribute an entry (vacuously OK).
	// SampleLimit = the cap applied to the cross-check diff. Identical
	//   across calls so clients can compute their own bounds.
	// SampleSaturated = the Qdrant scroll returned exactly cap rows AND the
	//   total point count exceeds cap; the diff numbers below are LOWER
	//   bounds, not absolute values.
	// CountsAreLowerBounds = same signal as SampleSaturated, surfaced under
	//   a different name for the JSON contract (operators found the latter
	//   less discoverable in the PR3-5b docs).
	QdrantHealthy        bool     `json:"qdrant_healthy"`
	ChecksComplete       bool     `json:"checks_complete"`
	Degraded             bool     `json:"degraded"`
	DegradedSources      []string `json:"degraded_sources,omitempty"`
	SampleLimit          int      `json:"sample_limit"`
	SampleSaturated      bool     `json:"sample_saturated"`
	CountsAreLowerBounds bool     `json:"counts_are_lower_bounds"`

	OK bool `json:"ok"`
}
