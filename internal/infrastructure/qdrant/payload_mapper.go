package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// PayloadMapper converts internal AssetData to Qdrant Point representations.
// It is the SINGLE place where vector names, payload fields, and embedding
// channel mapping are configured — no hardcoded names anywhere else.
type PayloadMapper struct {
	store AssetStore
	log   *zap.Logger
}

// NewPayloadMapper creates a PayloadMapper.
func NewPayloadMapper(store AssetStore, log *zap.Logger) *PayloadMapper {
	return &PayloadMapper{
		store: store,
		log:   log,
	}
}

// FetchAsset delegates to the AssetStore.
func (m *PayloadMapper) FetchAsset(ctx context.Context, assetID string) (*AssetData, error) {
	return m.store.FetchAsset(ctx, assetID)
}

// ListAllAssetIDs delegates to the AssetStore.
func (m *PayloadMapper) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return m.store.ListAllAssetIDs(ctx)
}

// AssetToPoint converts an AssetData to a Qdrant Point using the manifest.
//
// PR 6 (refactor/qdrant-index-document): AssetToPoint now delegates to
// AssetToIndexDocument → IndexDocumentToPoint so the canonical airlock
// is the single source of truth for the Wire shape. All the existing
// rules are preserved verbatim (QDRANT-003 + PR 2 BM25 + QDRANT-001 + PR 6 §7.4):
//
//   - Dense vector names come from the manifest, not hardcoded constants.
//   - Audio is included ONLY when the vector is present and the channel is active.
//   - Each vector's dimension is validated before the HTTP call.
//   - NaN/Inf values are rejected.
//   - Empty vectors for required channels are rejected.
//   - Assets with invalid vectors are NOT silently skipped — errors are typed.
//   - PR2 (fix/qdrant-bm25-indexing): transcript is NEVER a fallback for text;
//     if TranscriptVector is missing the channel is dropped, no synthetic
//     vector is shipped.
//   - QDRANT-001: the point ID is canonicalised via AssetIDToQdrantPointID
//     (UUID v5 SHA-1 with project-namespacing). The canonical asset_id is
//     read from point.Payload["asset_id"] when a point is retrieved.
//   - PR 6 verdict §7.4 footer: drive_link / local_path / status are
//     forbidden locator keys; the IndexDocument airlock strips them
//     from the wire.
//
// QDRANT-001 (June 2026): this is still the only legal site that derives a
// Qdrant point ID from a media_assets.id (via the canonical
// AssetIDToQdrantPointID call inside IndexDocumentToPoint).
func (m *PayloadMapper) AssetToPoint(asset *AssetData, schema *IndexSchema) (*Point, error) {
	doc, err := m.AssetToIndexDocument(asset, schema)
	if err != nil {
		return nil, err
	}
	return m.IndexDocumentToPoint(doc, schema)
}

// getVectorForChannel returns the embedding vector for a given channel.
func (m *PayloadMapper) getVectorForChannel(asset *AssetData, channel string) []float32 {
	switch channel {
	case "text":
		return asset.TextVector
	case "transcript":
		return asset.TranscriptVector
	case "visual":
		return asset.VisualVector
	case "audio":
		return asset.AudioVector
	default:
		return nil
	}
}

// BuildPayload constructs the Qdrant payload from an AssetData and schema.
//
// PR 6 (refactor/qdrant-index-document): BuildPayload now delegates to
// BuildPayloadFromDocument via the IndexDocument airlock so production
// writes satisfy verdict §11 (OBSERVED provenance). The wire shape is
// unchanged for legacy callers (assetToIndexDocumentNoValidate populates
// EmbeddingArtifacts whose ModelVersion defaults to the schema value;
// AssetData carries no separate observed source today).
//
// QDRANT-004 PR2 (June 2026): the canonical lifecycle key is
// `lifecycle_state` (SSOT — matches media_assets.lifecycle_state in SQLite,
// qdrant.DefaultV3Schema().PayloadIndexes, and the search adapter
// filter). The previous `status` key was a legacy alias from pre-QDRANT-004
// ingest pipelines; it has been retired from the writer
// (BuildPayloadFromDocument below), the reader (search_adapter,
// clip_search_adapter), and the manifest (DefaultV3Schema.PayloadIndexes).
// One-shot migration of historical points is the QDRANT-005B
// reconciler's repair path (target wave); until then legacy points carry
// both keys and the reader falls through silently.
//
// QDRANT-001 (June 2026) closure: drive_link is NEVER in the payload;
// the airlock strips it from AssetData.DriveLink → IndexDocument (no
// field there) → wire.
func BuildPayload(asset *AssetData, schema *IndexSchema) map[string]interface{} {
	return BuildPayloadFromDocument(assetToIndexDocumentNoValidate(asset, schema), schema)
}

// parseMetadataJSON lazily parses metadata JSON on first access.
func parseMetadataJSON(asset *AssetData) {
	if asset.Metadata != nil || asset.MetadataJSON == "" {
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(asset.MetadataJSON), &m); err == nil {
		asset.Metadata = m
	}
}

func isNaNOrInf(v float32) bool {
	return math.IsNaN(float64(v)) || math.IsInf(float64(v), 0)
}

// ══════════════════════════════════════════════════════════════════════════
// PR 6 (refactor/qdrant-index-document) — canonical Mapper airlock.
// ══════════════════════════════════════════════════════════════════════════

// BuildPayloadFromDocument is the canonical writer-side payload builder.
// Reads an IndexDocument (defined in index_document.go) and emits the
// canonical Qdrant payload.
//
// PR 6 (verdict §11): the per-channel embedding_version_<channel>
// payload keys are written from doc.Embeddings[channel].ModelVersion
// (the OBSERVED provenance recorded in the artifact), NOT from
// schema.DenseVectors.ModelVersion (the schema's EXPECTED). The schema
// declares the contract the operator wants; the artifact records what
// the writer actually emitted. Drift surface: the per-channel counter
// in SwitchReport.VersionMismatchPerChannel (PR 12).
//
// FORBIDDEN at this emitter: drive_link, local_path, status payload
// keys. The AirLock via IndexDocument strips these from the wire shape;
// this fn preserves the invariant. The freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocumentCanonicalTypes
// + the wire-level test below pin both halves of the invariant.
func BuildPayloadFromDocument(doc *IndexDocument, schema *IndexSchema) map[string]interface{} {
	if doc == nil {
		return map[string]interface{}{}
	}
	payload := map[string]interface{}{
		"asset_id":        doc.AssetID,
		"lifecycle_state": string(doc.LifecycleState),
		"source":          doc.Metadata.Source,
		"media_type":      doc.Metadata.MediaType,
	}

	if doc.WorkspaceID != "" {
		payload["workspace_id"] = doc.WorkspaceID
	}
	if doc.Metadata.Language != "" {
		payload["language"] = doc.Metadata.Language
	}
	if doc.Metadata.Category != "" {
		payload["category"] = doc.Metadata.Category
	}
	if doc.Metadata.Style != "" {
		payload["style"] = doc.Metadata.Style
	}
	if doc.Metadata.YouTubeChan != "" {
		payload["channel_id"] = doc.Metadata.YouTubeChan
	}
	if doc.Metadata.License != "" {
		payload["license"] = doc.Metadata.License
	}
	if doc.Metadata.DurationMs > 0 {
		payload["duration_ms"] = doc.Metadata.DurationMs
	}
	if doc.Metadata.IndexVersion != "" {
		payload["index_version"] = doc.Metadata.IndexVersion
	}
	if doc.SourceVersion != "" {
		payload["source_version"] = doc.SourceVersion
	}
	if doc.Metadata.CreatedAt != "" {
		payload["created_at"] = doc.Metadata.CreatedAt
	}
	if doc.Metadata.UpdatedAt != "" {
		payload["updated_at"] = doc.Metadata.UpdatedAt
	}
	if doc.Metadata.DeletedAt != "" {
		payload["deleted_at"] = doc.Metadata.DeletedAt
	}

	// Provenance WINS over schema (verdict §11). Per-artifact observed
	// version keys the on-disk payload. Empty observed versions are
	// SKIPPED so the verifier's per-channel counter surfaces the gap
	// loudly (non-silent failure) — a future PR that sources
	// observed-from-metadata_json will populate ModelVersion here and
	// the wire matches the source-of-truth automatically.
	for channel, artifact := range doc.Embeddings {
		if artifact.ModelVersion == "" {
			continue
		}
		payload[fmt.Sprintf("embedding_version_%s", string(channel))] = artifact.ModelVersion
	}

	if doc.Metadata.Title != "" {
		payload["title"] = doc.Metadata.Title
	}
	if len(doc.Metadata.Tags) > 0 {
		payload["tags"] = doc.Metadata.Tags
	}
	if doc.SearchText != "" {
		payload["search_text"] = doc.SearchText
	}
	if doc.Metadata.YouTubeID != "" {
		payload["youtube_video_id"] = doc.Metadata.YouTubeID
	}
	if doc.Metadata.YouTubeURL != "" {
		payload["youtube_url"] = doc.Metadata.YouTubeURL
	}
	if doc.Metadata.Description != "" {
		payload["description"] = doc.Metadata.Description
	}
	return payload
}

// assetToIndexDocumentNoValidate is the unguarded AssetData →
// IndexDocument airlock. Used by the legacy BuildPayload wrapper path
// AND as the substrate for the Mapper.AssetToIndexDocument validation
// path. Returns an IndexDocument whose Embeddings map is populated
// for BOTH sparse channels (Values=nil) AND dense channels (Values
// captured when present).
//
// Lifecycle state is normalised via domainAssetLifecycle (empty falls
// back to ACTIVE post-migration 101). Title/Description are populated
// from the parsed metadata_json bag.
//
// ModelVersion is the OBSERVED provenance (PR 6 verdict §11) and
// defaults to spec.ModelVersion since AssetData carries no separate
// observed source today; future PRs that source observed-from-
// metadata_json flip the source without touching this helper. The
// validation pass (dim/NaN) lives in AssetToIndexDocument; this helper
// deliberately skips validation so producers without vectors (legacy
// AssetData shims, debug replay paths) still emit a complete-per-
// schema payload.
func assetToIndexDocumentNoValidate(asset *AssetData, schema *IndexSchema) *IndexDocument {
	if asset == nil {
		return &IndexDocument{}
	}
	parseMetadataJSON(asset)

	// EmbeddingArtifact population needs the dimensional vectors; resolve
	// them once so we don't recurse via the Mapper from a free function.
	// The legacy build path uses AssetData vectors directly; the Mapper
	// airlock does dimension/NaN validation for the new BuildPayload
	// survivors. Both paths converge on the same per-channel artifacts.
	doc := &IndexDocument{
		AssetID:        asset.ID,
		WorkspaceID:    asset.WorkspaceID,
		LifecycleState: domainAssetLifecycle(asset.LifecycleState),
		SourceVersion:  asset.SourceVersion,
		ContentHash:    asset.ContentHash,
		SearchText:     asset.SearchText,
		Metadata: IndexedMetadata{
			Title:        metadataString(asset.Metadata, "title"),
			Description:  metadataString(asset.Metadata, "description"),
			Tags:         asset.Tags,
			Source:       asset.Source,
			MediaType:    asset.MediaType,
			Language:     asset.Language,
			Category:     asset.Category,
			Style:        asset.Style,
			DurationMs:   asset.DurationMs,
			YouTubeID:    asset.YouTubeVideoID,
			YouTubeURL:   asset.YouTubeURL,
			StartTime:    asset.StartTime,
			EndTime:      asset.EndTime,
			YouTubeChan:  asset.ChannelID,
			License:      asset.License,
			IndexVersion: asset.IndexVersion,
			CreatedAt:    asset.CreatedAt,
			UpdatedAt:    asset.UpdatedAt,
			DeletedAt:    asset.DeletedAt,
		},
		Embeddings: map[VectorChannel]EmbeddingArtifact{},
	}

	// Sparse channels: each declared sparse channel becomes an
	// EmbeddingArtifact (Model from spec, Values=nil — Qdrant does
	// server-side inference). SparseSpec carries no ModelVersion or
	// PreprocessVer — those fields live on EmbeddingSpec (dense) only.
	for _, spec := range schema.SparseVectors {
		channel := VectorChannel(spec.Channel)
		model := spec.Model
		if model == "" {
			model = DefaultSparseModel
		}
		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:    channel,
			Model:      model,
			Dimensions: -1, // sparse: Qdrant infers server-side
		}
	}

	// Dense channels: each schema-declared channel becomes an
	// EmbeddingArtifact. Values is populated when the AssetData has a
	// vector for that channel; nil when absent (IndexDocumentToPoint
	// drops the channel from the wire). ModelVersion is the OBSERVED
	// provenance (verdict §11); the validation pass lives in
	// AssetToIndexDocument.
	for _, spec := range schema.DenseVectors {
		channel := VectorChannel(spec.Channel)
		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:       channel,
			Model:         spec.Model,
			ModelVersion:  spec.ModelVersion, // OBSERVED (write-only provenance; defaults to schema today)
			PreprocessVer: spec.PreprocessVer,
			Dimensions:    spec.Dimensions,
		}
	}
	return doc
}

// domainAssetLifecycle converts a media_assets.lifecycle_state string
// to the canonical asset.LifecycleState. Empty → ACTIVE (legacy rows
// post-migration 101 fall through to ACTIVE; canonical fallback).
func domainAssetLifecycle(raw string) asset.LifecycleState {
	const fallback = "ACTIVE"
	if raw == "" {
		return asset.LifecycleState(fallback)
	}
	return asset.LifecycleState(raw)
}

// metadataString extracts a string-typed field from a parsed JSON
// metadata map. Returns "" when absent or non-string.
func metadataString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// AssetToIndexDocument is the canonical Mapper airlock (PR 6). Builds
// an IndexDocument from a SQL-fetch AssetData and validates the
// per-channel vector dimensions / NaN / Inf before the wire is
// constructed. Returns the same typed errors as AssetToPoint
// (ErrEmptyVector / ErrVectorDimensionMismatch / ErrNaNOrInf) so the
// upstream IndexWriter fail-closed at the type assertion already
// caught in BuildProcessBundle (`var _ clipindexer.VectorStoreIndexer
// = (*qdrant.IndexWriter)(nil)`) keeps behaving identically.
//
// The airlock strips Status / DriveLink / LocalPath at the
// IndexDocument boundary; the IndexDocument struct has no such fields,
// so the wire-shape invariant is enforced statically (frozen test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocument
// CanonicalTypes) AND dynamically (wire-shape test in
// payload_mapper_test.go::TestBuildPayloadFromDocument_NoForbidden
// LocatorKeys).
func (m *PayloadMapper) AssetToIndexDocument(asset *AssetData, schema *IndexSchema) (*IndexDocument, error) {
	if asset == nil {
		return nil, fmt.Errorf("asset is nil")
	}
	if asset.ID == "" {
		return nil, fmt.Errorf("asset ID must not be empty")
	}
	doc := assetToIndexDocumentNoValidate(asset, schema)
	for _, spec := range schema.DenseVectors {
		channel := VectorChannel(spec.Channel)
		vec := m.getVectorForChannel(asset, spec.Channel)
		// Channel-emptiness policy mirrors the existing AssetToPoint body:
		//   audio is optional (drop when absent),
		//   transcript is dropped on nil (NOT a fallback; PR 2 eliminated
		//     the synthetic text→transcript substitution),
		//   text is REQUIRED (ErrEmptyVector).
		if vec == nil {
			if spec.Channel == "audio" {
				continue
			}
			if spec.Channel == "transcript" {
				continue
			}
			if spec.Channel == "text" {
				return nil, &ErrEmptyVector{
					Channel: spec.Channel,
					AssetID: asset.ID,
				}
			}
			continue
		}
		if len(vec) != spec.Dimensions {
			return nil, &ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  asset.ID,
			}
		}
		for _, v := range vec {
			if isNaNOrInf(v) {
				return nil, &ErrNaNOrInf{
					Channel: spec.Channel,
					AssetID: asset.ID,
				}
			}
		}
		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:       channel,
			Values:        vec,
			Model:         spec.Model,
			ModelVersion:  spec.ModelVersion, // OBSERVED (write-only provenance; defaults to schema)
			PreprocessVer: spec.PreprocessVer,
			Dimensions:    spec.Dimensions,
			GeneratedAt:   time.Now(),
		}
	}
	return doc, nil
}

// IndexDocumentToPoint is the canonical wire-shaping layer (PR 6).
// Reads an IndexDocument (the airlock output) and emits the Qdrant
// Point shape. Mirrors the validation + sparse-channel handling of
// AssetToPoint on the IndexDocument side. Callers that already have
// an IndexDocument skip the AssetToIndexDocument step.
//
// Sparse-channel wire shape: Qdrant requires `{text, model}` for
// server-side BM25 inference; the bm25_text artifact records the
// model name; SearchText comes from the document. Empty SearchText
// drops the channel (mirrors the existing AssetToPoint behavior for
// backward compatibility with the PR 2 BM25 contract).
func (m *PayloadMapper) IndexDocumentToPoint(doc *IndexDocument, schema *IndexSchema) (*Point, error) {
	if doc == nil {
		return nil, fmt.Errorf("index document is nil")
	}
	if doc.AssetID == "" {
		return nil, fmt.Errorf("index document AssetID must not be empty")
	}
	vectors := make(map[string]interface{})
	for channel, artifact := range doc.Embeddings {
		switch channel {
		case ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio:
			if artifact.Values == nil {
				continue
			}
			vectors[string(channel)] = artifact.Values
		case ChannelBM25Text:
			if doc.SearchText == "" {
				if m.log != nil {
					m.log.Debug("sparse channel: no search_text in doc, channel dropped",
						zap.String("asset_id", doc.AssetID),
						zap.String("channel", string(channel)))
				}
				continue
			}
			vectors[string(channel)] = map[string]interface{}{
				"text":  doc.SearchText,
				"model": artifact.Model,
			}
		default:
			if artifact.Values != nil {
				vectors[string(channel)] = artifact.Values
			}
		}
	}
	return &Point{
		ID:      AssetIDToQdrantPointID(doc.AssetID),
		Vectors: vectors,
		Payload: BuildPayloadFromDocument(doc, schema),
	}, nil
}
