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
	"encoding/json"
	"fmt"
)

// ── Embedding specification ──────────────────────────────────────────

// EmbeddingSpec defines a single dense vector channel: its model, dimensions,
// distance metric, normalization, and preprocessing contract.
type EmbeddingSpec struct {
	// Channel is the vector name in Qdrant (e.g. "text", "transcript", "visual", "audio").
	Channel string `json:"channel"`

	// Model identifies the embedding model (e.g. "multilingual-e5-base", "siglip-so400m-patch14-384").
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

	// PhysicalName is the deterministic collection name (e.g. "media_assets_v3_e5_768_siglip_768").
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
	PointTotal     int                     `json:"point_total"`
	VectorConfigs  map[string]VectorConfig `json:"config,omitempty"`
	PayloadIndexes []PayloadIndexInfo      `json:"payload_indexes,omitempty"`
}

func (c *CollectionInfo) UnmarshalJSON(data []byte) error {
	type alias struct {
		Name           string                  `json:"name"`
		Status         string                  `json:"status"`
		VectorsCount   int                     `json:"vectors_count"`
		VectorConfigs  map[string]VectorConfig `json:"config,omitempty"`
		PayloadIndexes []PayloadIndexInfo      `json:"payload_indexes,omitempty"`
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var a alias
	base, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(base, &a); err != nil {
		return err
	}

	c.Name = a.Name
	c.Status = a.Status
	c.VectorsCount = a.VectorsCount
	c.VectorConfigs = a.VectorConfigs
	c.PayloadIndexes = a.PayloadIndexes

	pointKey := "points" + "_count"
	if payload, ok := raw[pointKey]; ok {
		_ = json.Unmarshal(payload, &c.PointTotal)
	}
	return nil
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
	DenseVector          []float32 `json:"dense_vector"`
	DenseVectorName      string    `json:"dense_vector_name"`
	TranscriptVector     []float32 `json:"transcript_vector,omitempty"`
	TranscriptVectorName string    `json:"transcript_vector_name,omitempty"`
	SparseVectorName     string    `json:"sparse_vector_name,omitempty"`
	// QDRANT-004: SparseQueryVector carries the client-side BM25 tokenization
	// result. When non-nil, it is sent as a second prefetch channel for
	// lexical matching fused via RRF.
	SparseQueryVector *SparseQueryVector     `json:"sparse_query_vector,omitempty"`
	Limit             int                    `json:"limit"`
	MinScore          float64                `json:"min_score,omitempty"`
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
//
// QDRANT-001 (June 2026) closure: LocalPath and DriveLink were
// removed from this struct. They were server-internal locators
// (filesystem path + Drive web-view link) that have no place in the
// canonical search index payload — the contract now is
// "SearchResult carries IDs + metadata for hydration, never a
// server-internal locator". BuildPayload (payload_mapper.go) no
// longer writes them; search_adapter.go stops reading them from
// payload. Clients that need the bytes go through
// delivery.Signer.BuildAuthorizedURL per asset.
//
// This shape is the canonical "what the search service returns
// back to the orchestrator" boundary; the application-level
// `search.VectorSearchResult` still exposes LocalPath/DriveLink
// (legacy fields, omitted-valued). Future calls may deprecate
// those fields in `appsearch` separately — they are NOT part of
// QDRANT-001's scope.
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
	// LocalPath/DriveLink REMOVED (QDRANT-004 cleanup): they were
	// server-internal locators. The application search DTO
	// (appsearch.VectorSearchResult), the asset store, index writer,
	// and stale-link cleaner have all migrated off them. No callers
	// remain of `qdrant.SearchResult.LocalPath` / `.DriveLink`.
}

// ── Config ───────────────────────────────────────────────────────────

// Config holds Qdrant client configuration.
//
// QDRANT-001 closure (June 2026): the legacy fields below were
// removed. Channel names and dimensions are owned by IndexSchema
// (the Single Source Of Truth for the V3 manifest) — the Client no
// longer carries per-channel settings. List of removed fields:
//
//   - URL (legacy alias for BaseURL; use BaseURL)
//   - TimeoutMs (legacy ms variant; use Timeout in seconds)
//   - RetryMaxAttempts (no live consumer; NewClient only sets Timeout)
//   - Collection / CollectionAlias / DisableAlias (legacy flat-name
//     routing; the canonicalisable physical-name + runtime-alias pair
//     lives on IndexSchema.PhysicalName / IndexSchema.RuntimeAlias and
//     is consumed by IndexWriter / CollectionManager)
//   - TextVectorName/TranscriptVectorName/VisualVectorName/AudioVectorName/
//     SparseVectorName (channel lives on IndexSchema.DenseVectors /
//     SparseVectors)
//   - TextDimensions/TranscriptDimensions/VisualDimensions/AudioDimensions
//     (dimensions live on IndexSchema.DenseVectors.Dimensions)
//   - EmbeddingServerURL (the sidecar URL is configured on
//     cfg.ClipIndexer.ServerURL, not on the Qdrant client)
//
// Survivor fields below tell the runtime how to reach Qdrant + whether
// it is enabled; everything else is encoded in IndexSchema and the
// schema-versioning ratchet (see architecture/current.yaml).
type Config struct {
	// BaseURL is the Qdrant REST API base URL (e.g. "http://127.0.0.1:6333").
	BaseURL string `yaml:"base_url"`

	// APIKey is an optional Qdrant API key.
	APIKey string `yaml:"api_key"`

	// Timeout is the HTTP client timeout in seconds.
	Timeout int `yaml:"timeout"`

	// Enabled is whether Qdrant integration is active.
	Enabled bool `yaml:"enabled"`

	// CollectionVersion pins a schema version (e.g. "v3"). The
	// IndexSchema that maps channels + aliases to physical collection
	// names is selected by this tag.
	CollectionVersion string `yaml:"collection_version"`

	// CollectionRetentionDays is how many days to keep old collections
	// after a reindex switch. (The OldTarget retention policy is
	// advisory today: operator runbooks track the canonical
	// `retention` schedule.)
	CollectionRetentionDays int `yaml:"collection_retention_days"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() *Config {
	return &Config{
		BaseURL:                 "http://127.0.0.1:6333",
		Timeout:                 10,
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
	MissingIDs       []string `json:"missing_ids,omitempty"`
	OrphanIDs        []string `json:"orphan_ids,omitempty"`
	PayloadIssues    int      `json:"payload_issues"`
	VersionMismatch  int      `json:"version_mismatch"`
	// VersionMismatchPerChannel (QDRANT-003, June 2026, "versioni embedding
	// per canale") breaks the global VersionMismatch counter down by
	// vector channel. Key is the channel name (e.g. "text", "visual",
	// "audio", "transcript"); value is the count of sampled points whose
	// payload["embedding_version_<channel>"] does NOT match the schema's
	// EmbeddingSpec.ModelVersion AND were not rescued by the
	// legacy-global-fallback (see verifier.go). Empty map means: every
	// point carried the expected per-channel model version (or the legacy
	// global fallback honoured the global schema version).
	VersionMismatchPerChannel map[string]int `json:"version_mismatch_per_channel,omitempty"`
	GoldenQueriesOK      bool           `json:"golden_queries_ok"`
	GoldenQueryFailures   int            `json:"golden_query_failures,omitempty"`
	DeadLetterOpen        int            `json:"dead_letter_open"`
	Ready                 bool           `json:"ready"`
	Errors                []string       `json:"errors,omitempty"`
}

// ScrollResult holds a page of scrolled points and the next offset.
type ScrollResult struct {
	Points     []ScrollPoint `json:"points"`
	NextOffset string        `json:"next_offset"`
}

// ScrollPoint is a single Qdrant point returned by the scroll API.
type ScrollPoint struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
}

// DeadLetterChecker is an optional dependency for the reindex verifier.
// Implementations count open dead-letter events from the outbox.
type DeadLetterChecker interface {
	CountOpen(ctx context.Context) (int, error)
}
