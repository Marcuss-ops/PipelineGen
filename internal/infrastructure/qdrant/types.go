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
	"bytes"
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

	// Modifier is the sparse vector type: "bm25", "splade".
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
//
// PR1 — fix/qdrant-wire-contracts: the public surface is unchanged so
// downstream consumers (CompareSchema, CollectionManager, readiness,
// admin CLI) keep their call sites. The MarshalJSON surface is the
// internal Qdrant wire envelope: nested under result.* with the
// canonical fields documented at https://api.qdrant.tech/api-reference/collections/get-collection.
//
// Why the change: pre-PR1 the decoder expected a flat shape
// (`name`, `status`, `vectors_count`, `config`, `payload_indexes`,
// `points_count` all at top level). Qdrant actually returns:
//   { "result": {
//       "status": "green",
//       "vectors_count": 1064,
//       "points_count": 1064,
//       "config": { "params": {
//           "vectors":         {<channel>: {"size": 768, "distance": "Cosine"}},
//           "sparse_vectors":  {<channel>: {"modifier": "bm25"}}
//       }},
//       "payload_schema": {<field>: {"data_type": "keyword"}}
//   }}
//
// The UnmarshalJSON below maps both nested paths into the same public
// fields so CompareSchema's `actual.VectorConfigs[name].Size` and
// `actual.PayloadIndexes[]` keep working byte-for-byte.
type CollectionInfo struct {
	Name           string                  `json:"name"`
	Status         string                  `json:"status"`
	VectorsCount   int                     `json:"vectors_count"`
	PointTotal     int                     `json:"points_count"`
	VectorConfigs  map[string]VectorConfig `json:"-"`
	PayloadIndexes []PayloadIndexInfo      `json:"-"`
	SparseConfigs  map[string]SparseConfig `json:"-"`
}

// SparseConfig mirrors Qdrant's per-sparse-vector configuration.
// Sparse vectors do not carry size/distance — they carry a modifier
// ("bm25" | "splade") and (when present) inference config for
// server-side embedding generation.
type SparseConfig struct {
	Modifier string `json:"modifier,omitempty"`
	Model    string `json:"model,omitempty"`
}

// UnmarshalJSON consumes Qdrant's nested `result.*` envelope.
//
// Reliability note: the outer object may itself be the leaf (test mocks)
// OR the Qdrant envelope `{"result": {...}}`. We treat both shapes as
// valid because the existing test surface in qdrant_test.go mocked the
// flat shape; production never sends that, but pre-PR1 callers may
// have raw payloads cached. The decoder picks the right shape via a
// presence probe on the "result" key.
func (c *CollectionInfo) UnmarshalJSON(data []byte) error {
	// Probe: do we have an envelope (`{"result":{...}}`) or are we
	// looking at the leaf (`{...}` directly)? The leaf shape is what
	// pre-PR1 tests/mocks emitted; the envelope is what real Qdrant
	// returns. The discriminator is the presence of `result` as a
	// top-level object key.
	var probe struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}

	// If `result` is a JSON object, unwrap it; otherwise treat the
	// whole payload as the leaf (legacy/mock shape). Booleans /
	// numbers / strings inside `result` are an error.
	//
	// PR1 fix (reviewer feedback): leading whitespace is legal per
	// RFC 8259 and Qdrant emits formatted JSON in some surfaces, so
	// probe.Result[0] could be a space/tab/newline, not '{'. Trim
	// the leading whitespace before the byte compare.
	trimmed := bytes.TrimLeft(probe.Result, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return c.unmarshalQdrantEnvelope(probe.Result)
	}
	return c.unmarshalLegacyLeaf(data)
}

// Compile-time assertion: CollectionInfo honours json.Unmarshaler so
// the canonical wire-shape decoding cannot drift from what callers
// expect at runtime.
var _ json.Unmarshaler = (*CollectionInfo)(nil)

// unmarshalQdrantEnvelope consumes the canonical `result.*` shape
// Qdrant returns:
//
//	{ "result": {
//	    "status": "green",
//	    "vectors_count": N,
//	    "points_count":  N,
//	    "config": { "params": {
//	        "vectors":        {<ch>: {"size": I, "distance": "Cosine"}},
//	        "sparse_vectors": {<ch>: {"modifier": "bm25"}}
//	    }},
//	    "payload_schema": {<field>: {"data_type": "..."}}
//	}}
func (c *CollectionInfo) unmarshalQdrantEnvelope(result json.RawMessage) error {
	// Re-marshal probe: contents of `result` may themselves contain a
	// nested `result` (defence in depth). We unwrap until reaching the
	// non-`result` wrapper.
	type sparseShape struct {
		Modifier string `json:"modifier,omitempty"`
		Model    string `json:"model,omitempty"`
	}
	type paramsShape struct {
		Vectors        map[string]VectorConfig `json:"vectors,omitempty"`
		SparseVectors  map[string]sparseShape  `json:"sparse_vectors,omitempty"`
	}
	type configShape struct {
		Params paramsShape `json:"params"`
	}
	type payloadSchemaField struct {
		DataType string `json:"data_type,omitempty"`
	}
	type resultShape struct {
		Name          string                       `json:"name"`
		Status        string                       `json:"status"`
		VectorsCount  int                          `json:"vectors_count"`
		PointsCount   int                          `json:"points_count"`
		Config        configShape                  `json:"config"`
		PayloadSchema map[string]payloadSchemaField `json:"payload_schema"`
	}
	var r resultShape
	if err := json.Unmarshal(result, &r); err != nil {
		return fmt.Errorf("decode qdrant collection envelope: %w", err)
	}

	c.Name = r.Name
	c.Status = r.Status
	c.VectorsCount = r.VectorsCount
	c.PointTotal = r.PointsCount
	c.VectorConfigs = r.Config.Params.Vectors
	if c.VectorConfigs == nil {
		c.VectorConfigs = make(map[string]VectorConfig)
	}

	// Sparse Vectors: map modifier/model onto the public struct.
	c.SparseConfigs = make(map[string]SparseConfig, len(r.Config.Params.SparseVectors))
	for ch, sv := range r.Config.Params.SparseVectors {
		c.SparseConfigs[ch] = SparseConfig{Modifier: sv.Modifier, Model: sv.Model}
	}

	// payload_schema is a map keyed by field name; flatten to a list of
	// PayloadIndexInfo so CompareSchema can range over it unchanged.
	c.PayloadIndexes = make([]PayloadIndexInfo, 0, len(r.PayloadSchema))
	for field, info := range r.PayloadSchema {
		c.PayloadIndexes = append(c.PayloadIndexes, PayloadIndexInfo{
			FieldName: field,
			FieldType: info.DataType,
		})
	}
	// Stable order for deterministic diff output in CompareSchema.
	sortPayloadIndexes(c.PayloadIndexes)
	return nil
}

// unmarshalLegacyLeaf consumes the pre-PR1 / mock flat shape used by
// the existing test surface (qdrant_test.go). It is documented as
// legacy and removed once test fixtures migrate; callers should keep
// emitting the canonical Qdrant envelope.
func (c *CollectionInfo) unmarshalLegacyLeaf(data []byte) error {
	type alias struct {
		Name           string                  `json:"name"`
		Status         string                  `json:"status"`
		VectorsCount   int                     `json:"vectors_count"`
		VectorConfigs  map[string]VectorConfig `json:"config,omitempty"`
		PayloadIndexes []PayloadIndexInfo      `json:"payload_indexes,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("decode qdrant collection (legacy leaf): %w", err)
	}
	c.Name = a.Name
	c.Status = a.Status
	c.VectorsCount = a.VectorsCount
	c.VectorConfigs = a.VectorConfigs
	if c.VectorConfigs == nil {
		c.VectorConfigs = make(map[string]VectorConfig)
	}
	c.PayloadIndexes = a.PayloadIndexes
	c.PointTotal = 0 // the legacy shape did not include points_count
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

// sortPayloadIndexes sorts the slice by FieldName for deterministic
// diff output (so CompareSchema's MissingIndexes / ExtraIndexes lists
// match the same order between two otherwise equal CollectionInfo
// values, regardless of the JSON object map iteration order). Keeps
// signature stable for call-site readability.
func sortPayloadIndexes(items []PayloadIndexInfo) {
	// Inline insertion sort — slices are small (tens of fields).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].FieldName > items[j].FieldName; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
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
//
// PR2 (fix/qdrant-bm25-indexing): server-side BM25 inference is
// the canonical strategy. The orchestrator passes the raw query
// text via SparseText (plus SparseModel, defaulting to
// DefaultSparseModel) and Qdrant tokenizes + weights + projects the
// sparse vector ON THE SERVER. The legacy client-side Raw vector
// path (SparseQueryVector) is preserved for diagnostic / bulk-from-csv
// flows that already have a pre-computed sparse representation; live
// retrieval MUST go through SparseText. See pkg/bm25 for the
// deprecation status of the client-side tokenizer.
type HybridSearchRequest struct {
	DenseVector          []float32 `json:"dense_vector"`
	DenseVectorName      string    `json:"dense_vector_name"`
	TranscriptVector     []float32 `json:"transcript_vector,omitempty"`
	TranscriptVectorName string    `json:"transcript_vector_name,omitempty"`
	SparseVectorName     string    `json:"sparse_vector_name,omitempty"`
	// SparseText (preferred, PR2+): raw text that Qdrant tokenizes
	// server-side via the SparseModel. Empty SparseText falls through
	// to the raw SparseQueryVector path (kept for diagnostic / bulk
	// flows only).
	SparseText string `json:"sparse_text,omitempty"`
	// SparseModel is the inference model used to project SparseText
	// into a sparse vector. Empty defaults to DefaultSparseModel.
	SparseModel string `json:"sparse_model,omitempty"`
	// SparseQueryVector carries the legacy client-side BM25
	// tokenization result (only used when SparseText is empty).
	// Kept for diagnostic / bulk-from-csv paths; production
	// orchestrators should set SparseText and let Qdrant handle
	// tokenization against the model configured on the sparse
	// channel.
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
// QDRANT-001 (June 2026): LocalPath and DriveLink have been
// removed from this struct AND from appsearch.VectorSearchResult.
// Server-internal locators (filesystem path + Drive web-view link)
// have no place in the canonical search contract — the rule is
// "SearchResult carries IDs + metadata for hydration, never a
// server-internal locator". BuildPayload no longer writes them;
// search_adapter.go no longer reads them. Clients that need bytes
// go through delivery.Signer.BuildAuthorizedURL per asset.
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

// (PR 4, June 2026, refactor/single-qdrant-runtime) The previous
// qdrant-side `QdrantDeleter` interface was deleted. The application
// layer's canonical VectorPointDeleter port (in
// internal/application/jobs/outbox/ports.go) is satisfied by
// *IndexWriter via the compile-time assertion in index_writer.go.
// Keeping the interface declaration in infra would re-introduce the
// pre-PR4 dual-interface duplication the verdict explicitly forbids.

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
//
// PR 12 (June 2026) extensions: three new fields expose the strict
// gate footprint that PR 12 enforces:
//
//   - CompleteScan: true iff every scroll page succeeded AND the
//     maxScrolls safety cap was not hit AND the trailing NextOffset
//     was empty at the end of the loop. Mirrors PR 10's
//     ScannedTotals.CompleteScan vocabulary so the JSON-shape
//     surface is consistent across the (reconciler, verifier)
//     couple. False ⇒ the report is partial; consumers must NOT
//     gate on counts in that case.
//
//   - TotalScrolled: the canonical number of points observed by
//     the verifier. Differs from ActualPoints only in degenerate
//     cases (e.g. the verifier scanned fewer points than the
//     CountPoints endpoint reported due to early-failure). The
//     strict pt.ID and per-channel gates apply on TotalScrolled;
//     if TotalScrolled != ActualPoints the OPERATOR must inspect
//     Errors — the count is suspect.
//
//   - NonCanonicalPointCount + NonCanonicalPointIDs: pt.ID must
//     equal AssetIDToQdrantPointID(payload["asset_id"]) literally.
//     A generic UUID-parseable id (the previous behaviour) is no
//     longer accepted — only the canonical boundary can locate a
//     Qdrant point via our reverse-mapping, so a generic-UUID
//     substitute silently lost the read path.
type SwitchReport struct {
	TargetCollection string `json:"target_collection"`
	ExpectedPoints   int    `json:"expected_points"`
	ActualPoints     int    `json:"actual_points"`
	// CompleteScan: see type doc. Initialised to false; flipped true
	// only when the verifier ran the full scroll loop without any
	// truncating condition (page error | cap hit | trailing NextOffset).
	CompleteScan    bool     `json:"complete_scan"`
	TotalScrolled   int      `json:"total_scrolled"`
	MissingCount    int      `json:"missing_count"`
	OrphanCount     int      `json:"orphan_count"`
	MissingIDs      []string `json:"missing_ids,omitempty"`
	OrphanIDs       []string `json:"orphan_ids,omitempty"`
	PayloadIssues   int      `json:"payload_issues"`
	VersionMismatch int      `json:"version_mismatch"`
	// VersionMismatchPerChannel (QDRANT-003, June 2026, "versioni embedding
	// per canale") breaks the global VersionMismatch counter down by
	// vector channel. Key is the channel name (e.g. "text", "visual",
	// "audio", "transcript"); value is the count of sampled points whose
	// payload["embedding_version_<channel>"] does NOT match the schema's
	// EmbeddingSpec.ModelVersion AND were not rescued by the
	// legacy-global-fallback (see verifier.go). Empty map means: every
	// point carried the expected per-channel model version (or the legacy
	// global fallback honoured the global schema version).
	//
	// PR 12: the per-channel check runs on EVERY scrolled page (no
	// 1000-point sample). The per-channel counter therefore reflects
	// the full collection, not a sample.
	VersionMismatchPerChannel map[string]int `json:"version_mismatch_per_channel,omitempty"`
	GoldenQueriesOK           bool           `json:"golden_queries_ok"`
	FiltersOK                 bool           `json:"filters_ok"`
	DeadLetterOpen            int            `json:"dead_letter_open"`
	// NonCanonicalPointCount + NonCanonicalPointIDs (PR 12): points
	// whose pt.ID != AssetIDToQdrantPointID(payload["asset_id"]). Any
	// such point is BLOCKING — the alias switch must not proceed.
	NonCanonicalPointCount int `json:"non_canonical_point_count"`
	// NonCanonicalTruncated is true when NonCanonicalPointIDs' slice
	// cap-threshold (currently 20) truncated the canonical-list
	// entries below NonCanonicalPointCount. Operators reading the
	// JSON should consult the count first; the slice is a
	// sample-of-the-first-20 for human-readable diagnostics.
	NonCanonicalTruncated bool     `json:"non_canonical_truncated,omitempty"`
	NonCanonicalPointIDs  []string `json:"non_canonical_point_ids,omitempty"`
	// RollbackTarget (PR 13) carries the active alias target that
	// was in place BEFORE the verification attempt. On Ready=false
	// the operator's PR 13 path retains this collection so a future
	// --apply can re-promote it (or a manual alias switch undoes the
	// blue-green swap). Empty when the cmd path cannot resolve the
	// active alias (e.g. recovery from a missing-runtime-alias scenario).
	RollbackTarget string `json:"rollback_target,omitempty"`
	// OldCollection is the timestamped collection the blue-green
	// reindex was ABOUT TO swap away from (PR 13). Set by command
	// pre-switch; the suffix distinguishes "currently active" from
	// "previously active" rolling snapshots.
	OldCollection string   `json:"old_collection,omitempty"`
	Ready         bool     `json:"ready"`
	Errors        []string `json:"errors,omitempty"`
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

// ── Locator cleanup (QDRANT-005) ────────────────────────────────────

// LocatorCleanupReport is the machine-readable result of a locator
// payload cleanup scan (dry-run or apply). It is the canonical artefact
// produced by LocatorCleaner.CleanLocators.
type LocatorCleanupReport struct {
	// DryRun is true when no mutations were performed.
	DryRun bool `json:"dry_run"`

	// Collection is the Qdrant collection that was scanned.
	Collection string `json:"collection"`

	// TotalPointsScrolled is the total number of points examined.
	TotalPointsScrolled int `json:"total_points_scrolled"`

	// PointsWithDriveLink is the count of points whose payload still
	// contained the legacy "drive_link" key.
	PointsWithDriveLink int `json:"points_with_drive_link"`

	// PointsWithLocalPath is the count of points whose payload still
	// contained the legacy "local_path" key.
	PointsWithLocalPath int `json:"points_with_local_path"`

	// PointsAffected is the number of distinct points with at least
	// one legacy key (drive_link OR local_path).
	PointsAffected int `json:"points_affected"`

	// KeysRemoved is the total number of payload key deletions sent
	// to the Qdrant API. In dry-run mode this is zero.
	KeysRemoved int `json:"keys_removed"`

	// BatchCount is the number of batch payload/delete calls made.
	BatchCount int `json:"batch_count"`

	// Errors contains any non-fatal errors encountered during scroll
	// or delete phases.
	Errors []string `json:"errors,omitempty"`
}

// GoldenQueryRunner is the port verifier.go uses to gate the "golden queries" block in the
// SwitchReport. It is intentionally an empty-marker interface here so callers can pass `nil`
// when no runner is wired yet, AND so a real concrete (e.g. http-based runner) can be added
// in a follow-up without touching verifier.go's signature again. See verifier_test.go for the
// canonical nil-passing usage.
type GoldenQueryRunner interface {
	IsEmptyMarker()
}
