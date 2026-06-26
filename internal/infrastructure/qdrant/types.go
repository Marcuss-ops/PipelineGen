// Package qdrant provides the canonical Qdrant vector-database infrastructure
// for PipelineGen. It implements schema-versioned collections, atomic aliases,
// configurable payload mapping, and real-model embedding contracts as specified
// by QDRANT-003.
//
// Architecture:
//   - Physical collections are immutable w.r.t. vector schema.
//   - Breaking changes create a new physical collection.
//   - Runtime reads/writes go through a canonical alias.
//   - Alias switch only after reindex and verification.
//   - SQLite holds the canonical index version per asset.
//   - No synthetic/fake vectors are ever written.
package qdrant

import (
	"context"
	"fmt"
)

// ── Embedding specification ──────────────────────────────────────────

// EmbeddingSpec defines a single dense vector channel: its model, dimensions,
// distance metric, normalization, and preprocessing contract.
type EmbeddingSpec struct {
	// Channel is the vector name in Qdrant (e.g. "text", "transcript", "visual", "audio").
	Channel string `json:"channel"`

	// Model identifies the embedding model (e.g. "multilingual-e5-base", "clip-ViT-B-32").
	Model string `json:"model"`

	// ModelVersion is the model release or fine-tune version (e.g. "2026-06-16-v1").
	ModelVersion string `json:"model_version"`

	// Dimensions is the output vector size (e.g. 768 for e5-base).
	Dimensions int `json:"dimensions"`

	// Distance is the Qdrant distance metric: "Cosine", "Euclid", "Dot".
	Distance string `json:"distance"`

	// Normalized is true when the model produces L2-normalized vectors.
	Normalized bool `json:"normalized"`

	// PreprocessVer identifies the text preprocessing pipeline version.
	PreprocessVer string `json:"preprocess_ver,omitempty"`

	// QueryPrefix is the prefix prepended to query text (e.g. "query: " for E5 models).
	QueryPrefix string `json:"query_prefix,omitempty"`

	// IndexPrefix is the prefix prepended to text during indexing (e.g. "passage: " for E5).
	IndexPrefix string `json:"index_prefix,omitempty"`
}

// SparseSpec defines a sparse vector channel (BM25, SPLADE, etc.).
type SparseSpec struct {
	// Channel is the vector name in Qdrant (e.g. "bm25_text").
	Channel string `json:"channel"`

	// Modifier is the sparse vector type: "bm25", "splade".
	Modifier string `json:"modifier"`
}

// PayloadIndexSpec defines a payload field index.
type PayloadIndexSpec struct {
	// FieldName is the payload key (e.g. "workspace_id", "status").
	FieldName string `json:"field_name"`

	// FieldType is the Qdrant index type: "keyword", "integer", "float", "datetime", "geo", "text".
	FieldType string `json:"field_type"`
}

// ── Index schema ─────────────────────────────────────────────────────

// IndexSchema is the canonical definition of a Qdrant collection structure.
// There is ONE canonical instance per schema version (e.g. DefaultV3Schema()).
type IndexSchema struct {
	// Version is the schema version string (e.g. "v3").
	Version string `json:"version"`

	// PhysicalName is the deterministic collection name (e.g. "media_assets_v3_e5_768_clip_512").
	PhysicalName string `json:"physical_name"`

	// RuntimeAlias is the alias used by all read/write operations (e.g. "media_assets_current").
	RuntimeAlias string `json:"runtime_alias"`

	// DenseVectors lists all named dense vector channels in this schema.
	DenseVectors []EmbeddingSpec `json:"dense_vectors"`

	// SparseVectors lists all sparse vector channels.
	SparseVectors []SparseSpec `json:"sparse_vectors,omitempty"`

	// PayloadIndexes lists payload field indexes to create on the collection.
	PayloadIndexes []PayloadIndexSpec `json:"payload_indexes,omitempty"`
}

// PhysicalName derives the deterministic physical collection name from the schema.
func (s *IndexSchema) physicalName() string {
	if s.PhysicalName != "" {
		return s.PhysicalName
	}
	return fmt.Sprintf("media_assets_%s", s.Version)
}

// ── Collection info (from Qdrant inspection) ─────────────────────────

// CollectionInfo describes a Qdrant collection as seen by the REST API.
type CollectionInfo struct {
	Name           string                  `json:"name"`
	Status         string                  `json:"status"`
	VectorsCount   int                     `json:"vectors_count"`
	PointsCount    int                     `json:"points_count"`
	VectorConfigs  map[string]VectorConfig `json:"config,omitempty"`
	PayloadIndexes []PayloadIndexInfo      `json:"payload_indexes,omitempty"`
}

// VectorConfig mirrors Qdrant's per-vector configuration.
type VectorConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

// PayloadIndexInfo describes a single payload index.
type PayloadIndexInfo struct {
	FieldName string `json:"field_name"`
	FieldType string `json:"field_type"`
}

// SchemaDiff reports the differences between expected and actual schemas.
type SchemaDiff struct {
	Compatible          bool            `json:"compatible"`
	MissingVectors      []string        `json:"missing_vectors,omitempty"`
	ExtraVectors        []string        `json:"extra_vectors,omitempty"`
	DimensionMismatches []DimensionDiff `json:"dimension_mismatches,omitempty"`
	DistanceMismatches  []DistanceDiff  `json:"distance_mismatches,omitempty"`
	MissingIndexes      []string        `json:"missing_indexes,omitempty"`
	ExtraIndexes        []string        `json:"extra_indexes,omitempty"`
}

// DimensionDiff records a vector whose dimensions don't match expectations.
type DimensionDiff struct {
	Channel  string `json:"channel"`
	Expected int    `json:"expected"`
	Actual   int    `json:"actual"`
}

// DistanceDiff records a vector whose distance metric doesn't match.
type DistanceDiff struct {
	Channel  string `json:"channel"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// ── Point types ──────────────────────────────────────────────────────

// Point is a single Qdrant point ready for upsert.
// Note: the Vectors field uses the Qdrant REST API key "vector" (singular).
type Point struct {
	ID      string                 `json:"id"`
	Vectors map[string]interface{} `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

// ── Search types ─────────────────────────────────────────────────────

// SearchRequest is a canonical ANN search request.
type SearchRequest struct {
	QueryVector []float32              `json:"vector"`
	VectorName  string                 `json:"vector_name"`
	Limit       int                    `json:"limit"`
	MinScore    float64                `json:"min_score,omitempty"`
	Filter      map[string]interface{} `json:"filter,omitempty"`

	// Convenience filter fields — set directly instead of building a Filter map.
	// If Filter is also set, the combination is implementation-defined.
	Source    string `json:"-"`
	Category  string `json:"-"`
	MediaType string `json:"-"`
	Language  string `json:"-"`
}

// HybridSearchRequest combines dense + sparse for hybrid retrieval.
type HybridSearchRequest struct {
	DenseVector          []float32              `json:"dense_vector"`
	DenseVectorName      string                 `json:"dense_vector_name"`
	TranscriptVector     []float32              `json:"transcript_vector,omitempty"`
	TranscriptVectorName string                 `json:"transcript_vector_name,omitempty"`
	SparseVectorName     string                 `json:"sparse_vector_name,omitempty"`
	// QDRANT-004: SparseQueryVector carries the client-side BM25 tokenization
	// result. When non-nil, it is sent as a second prefetch channel for
	// lexical matching fused via RRF.
	SparseQueryVector *SparseQueryVector `json:"sparse_query_vector,omitempty"`
	Limit             int                `json:"limit"`
	MinScore          float64            `json:"min_score,omitempty"`
	Filter            map[string]interface{} `json:"filter,omitempty"`

	// Convenience filter fields.
	Source    string `json:"-"`
	Category  string `json:"-"`
	MediaType string `json:"-"`
	Language  string `json:"-"`
}

// SparseQueryVector is a Qdrant-compatible sparse vector for hybrid search.
// Indices are hashed token IDs; Values are term-frequency scores in (0, 1].
type SparseQueryVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

// SearchResult is a single match from Qdrant.
// Raw fields (ID, Score, Payload, Version) come directly from the Qdrant API.
// Derived fields (AssetID, Name, …) are populated from Payload by convenience
// helpers (searchResultFromPoint, etc.).
type SearchResult struct {
	// Raw Qdrant fields.
	ID      string                 `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]interface{} `json:"payload,omitempty"`
	Version int64                  `json:"version,omitempty"`

	// Derived convenience fields (populated from Payload).
	AssetID        string   `json:"asset_id,omitempty"`
	QdrantPointID  string   `json:"qdrant_point_id,omitempty"`
	Source         string   `json:"source,omitempty"`
	Name           string   `json:"name,omitempty"`
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
}

// ── Config ───────────────────────────────────────────────────────────

// Config holds Qdrant client configuration.
type Config struct {
	// ── Connection ──

	// BaseURL is the Qdrant REST API base URL (e.g. "http://127.0.0.1:6333").
	BaseURL string `yaml:"base_url"`

	// URL is a legacy alias for BaseURL.
	URL string `yaml:"url"`

	// APIKey is an optional Qdrant API key.
	APIKey string `yaml:"api_key"`

	// Timeout is the HTTP client timeout in seconds (new code).
	Timeout int `yaml:"timeout"`

	// TimeoutMs is the HTTP client timeout in milliseconds (legacy code).
	TimeoutMs int `yaml:"timeout_ms"`

	// RetryMaxAttempts is the maximum number of retries for transient failures.
	RetryMaxAttempts int `yaml:"retry_max_attempts"`

	// ── Feature flags ──

	// Enabled is whether Qdrant integration is active.
	Enabled bool `yaml:"enabled"`

	// ── Collection ──

	// Collection is the base collection name (e.g. "media_assets").
	Collection string `yaml:"collection"`

	// CollectionVersion pins a schema version (e.g. "v3").
	CollectionVersion string `yaml:"collection_version"`

	// CollectionAlias is the runtime alias for reads/writes.
	CollectionAlias string `yaml:"collection_alias"`

	// DisableAlias forces direct physical-collection access.
	DisableAlias bool `yaml:"disable_alias"`

	// CollectionRetentionDays is how many days to keep old collections after switch.
	CollectionRetentionDays int `yaml:"collection_retention_days"`

	// ── Vector names and dimensions ──

	// TextVectorName is the dense vector name for semantic text (e.g. "text").
	TextVectorName string `yaml:"text_vector_name"`

	// TranscriptVectorName is the dense vector name for transcripts.
	TranscriptVectorName string `yaml:"transcript_vector_name"`

	// VisualVectorName is the dense vector name for visual embeddings.
	VisualVectorName string `yaml:"visual_vector_name"`

	// AudioVectorName is the dense vector name for audio embeddings.
	AudioVectorName string `yaml:"audio_vector_name"`

	// SparseVectorName is the sparse vector name (e.g. "bm25_text").
	SparseVectorName string `yaml:"sparse_vector_name"`

	// TextDimensions is the vector size for text embeddings.
	TextDimensions int `yaml:"text_dimensions"`

	// TranscriptDimensions is the vector size for transcript embeddings.
	TranscriptDimensions int `yaml:"transcript_dimensions"`

	// VisualDimensions is the vector size for visual embeddings.
	VisualDimensions int `yaml:"visual_dimensions"`

	// AudioDimensions is the vector size for audio embeddings.
	AudioDimensions int `yaml:"audio_dimensions"`

	// ── Embedding ──

	// EmbeddingServerURL is the sidecar URL for text embeddings.
	EmbeddingServerURL string `yaml:"embedding_server_url"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() *Config {
	return &Config{
		BaseURL:                 "http://127.0.0.1:6333",
		Timeout:                 10,
		RetryMaxAttempts:        3,
		CollectionRetentionDays: 7,
	}
}

// ── Health / diagnostics types ──────────────────────────────────────
type IndexHealthReport struct {
	OK              bool `json:"ok"`
	Degraded        bool `json:"degraded,omitempty"`
	QdrantPoints    int  `json:"qdrant_points"`
	DBTotal         int  `json:"db_total"`
	WithEmbedding   int  `json:"with_embedding"`
	DBToQdrantDelta int  `json:"db_to_qdrant_delta"`
}

// CurrentEmbeddingVersion is the canonical embedding schema version.
const CurrentEmbeddingVersion = "v3"

// CurrentSearchTextVersion is the canonical search-text generation version.
const CurrentSearchTextVersion = "v3"

// IndexWriterPort is the contract for indexing clips into Qdrant (used by clipindexer).
// Concrete implementation: *IndexWriter.
type IndexWriterPort interface {
	UpsertFromClip(ctx context.Context, clipID string) error
	UpsertFromClips(ctx context.Context, clipIDs []string) error
}

// QdrantDeleter is the contract for deleting points from Qdrant (used by outbox).
// Concrete implementation: *IndexWriter.
type QdrantDeleter interface {
	DeletePoints(ctx context.Context, ids []string) error
}

// ── Reindex types ────────────────────────────────────────────────────

// ReindexResult holds the outcome of a reindex operation.
type ReindexResult struct {
	TotalAssets      int      `json:"total_assets"`
	IndexedAssets    int      `json:"indexed_assets"`
	FailedAssets     int      `json:"failed_assets"`
	FailedAssetIDs   []string `json:"failed_asset_ids,omitempty"`
	TargetCollection string   `json:"target_collection"`
	DryRun           bool     `json:"dry_run"`
}

// SwitchReport is the pre-switch verification report.
type SwitchReport struct {
	TargetCollection string   `json:"target_collection"`
	ExpectedPoints   int      `json:"expected_points"`
	ActualPoints     int      `json:"actual_points"`
	MissingCount     int      `json:"missing_count"`
	OrphanCount      int      `json:"orphan_count"`
	GoldenQueriesOK  bool     `json:"golden_queries_ok"`
	FiltersOK        bool     `json:"filters_ok"`
	DeadLetterOpen   int      `json:"dead_letter_open"`
	Ready            bool     `json:"ready"`
	Errors           []string `json:"errors,omitempty"`
}
