// schema_types.go — Qdrant schema-level types (June 2026, PR3 split).
//
// PR3 mechanical split (June 2026): relocated from types.go without
// signature or behaviour changes. These types form the canonical
// schema-versioned manifest; IndexSchema is the Single Source Of
// Truth for the V3 collection structure (channels + aliases +
// payload indexes) read by IndexWriter, CollectionManager, and the
// admin CLI.
//
// Channel names, dimensions, server-side model spec, and payload
// index specs all live here. The companion Config holds only the
// runtime cross-cutting fields (BaseURL, APIKey, Timeout, Enabled,
// CollectionVersion, retention). In sync with collection_types.go.
package schema

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

// EmbeddingContract is the runtime/indexing identity of a dense channel.
// Dimension alone is not sufficient: two models can emit 768 values while
// producing incompatible vector spaces.
type EmbeddingContract struct {
	Model         string
	ModelVersion  string
	Dimensions    int
	Distance      string
	Normalized    bool
	QueryPrefix   string
	IndexPrefix   string
	PreprocessVer string
}

func (e EmbeddingSpec) Contract() EmbeddingContract {
	return EmbeddingContract{
		Model: e.Model, ModelVersion: e.ModelVersion, Dimensions: e.Dimensions,
		Distance: e.Distance, Normalized: e.Normalized,
		QueryPrefix: e.QueryPrefix, IndexPrefix: e.IndexPrefix,
		PreprocessVer: e.PreprocessVer,
	}
}

func (e EmbeddingSpec) MatchesContract(got EmbeddingContract) bool {
	return e.Contract() == got
}

// SparseSpec defines a sparse vector channel (BM25, SPLADE, etc.).
//
// PR2 (fix/qdrant-bm25-indexing, June 2026): Model is the inference
// model name sent in the sparse_vectors config at create time and
// reused at query time. Default is "qdrant/bm25" (server-side BM25
// inference). Without Model the sparse channel cannot be created
// against modern Qdrant — the modify-only-by-modifier payload was
// silently rejected in Qdrant 1.10+.
type SparseSpec struct {
	// Channel is the vector name in Qdrant (e.g. "bm25_text").
	Channel string `json:"channel"`

	// Modifier is the sparse vector type: "idf", "splade".
	Modifier string `json:"modifier"`

	// Model is the server-side inference model used for this sparse
	// channel. Qdrant uses it at upsert time (to project text → sparse
	// vector) and at query time (to project query text → sparse vector).
	// Empty falls back to DefaultSparseModel().
	Model string `json:"model,omitempty"`
}

// DefaultSparseModel is the canonical server-side BM25 model name.
// PipelineGen always uses this for the bm25_text channel so the
// indexing path and the query path tokenize against the same vocab.
const DefaultSparseModel = "qdrant/bm25"

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

	// PhysicalName is the deterministic collection name for a projection
	// generation and its embedding contract.
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

// DeepCopy returns a deep copy of the IndexSchema suitable for mutation
// without affecting the original. All slices are allocated fresh; scalar
// fields are value-copied. EmbeddingSpec, SparseSpec, and PayloadIndexSpec
// are value types so struct copies are sufficient.
//
// PR #11 (July 2026): the SchemaRegistry now returns DeepCopy'd schemas
// from Resolve() so post-boot callers cannot mutate the registry's
// internal canonical instances.
func (s *IndexSchema) DeepCopy() *IndexSchema {
	if s == nil {
		return nil
	}
	c := &IndexSchema{
		Version:      s.Version,
		PhysicalName: s.PhysicalName,
		RuntimeAlias: s.RuntimeAlias,
	}
	if len(s.DenseVectors) > 0 {
		c.DenseVectors = make([]EmbeddingSpec, len(s.DenseVectors))
		copy(c.DenseVectors, s.DenseVectors)
	}
	if len(s.SparseVectors) > 0 {
		c.SparseVectors = make([]SparseSpec, len(s.SparseVectors))
		copy(c.SparseVectors, s.SparseVectors)
	}
	if len(s.PayloadIndexes) > 0 {
		c.PayloadIndexes = make([]PayloadIndexSpec, len(s.PayloadIndexes))
		copy(c.PayloadIndexes, s.PayloadIndexes)
	}
	return c
}

// CanonicalName derives the deterministic physical collection name. Exported for cross-package access.
func (s *IndexSchema) CanonicalName() string { return s.physicalName() }

// physicalName derives the deterministic physical collection name from the schema.
func (s *IndexSchema) physicalName() string {
	if s.PhysicalName != "" {
		return s.PhysicalName
	}
	return fmt.Sprintf("media_assets_%s", s.Version)
}

// ── Schema versioning constants ──────────────────────────────────────

// CurrentEmbeddingVersion is the canonical embedding schema version.
const CurrentEmbeddingVersion = "v3"

// CurrentSearchTextVersion is the canonical search-text generation version.
const CurrentSearchTextVersion = "v3"

// ── Health / diagnostics types ──────────────────────────────────────

// IndexHealthReport is the diagnostics report returned by the readiness
// barrier. Lives here (next to IndexSchema) because both are consumed by
// the readiness barrier and the operator dashboard.
type IndexHealthReport struct {
	OK              bool `json:"ok"`
	Degraded        bool `json:"degraded,omitempty"`
	QdrantPoints    int  `json:"qdrant_points"`
	DBTotal         int  `json:"db_total"`
	WithEmbedding   int  `json:"with_embedding"`
	DBToQdrantDelta int  `json:"db_to_qdrant_delta"`
}

// ── Port surface ─────────────────────────────────────────────────────

// IndexWriterPort is the contract for indexing clips into Qdrant (used by clipindexer).
// Concrete implementation: *IndexWriter.
type IndexWriterPort interface {
	UpsertFromClip(ctx context.Context, clipID string) error
	UpsertFromClips(ctx context.Context, clipIDs []string) error
}
