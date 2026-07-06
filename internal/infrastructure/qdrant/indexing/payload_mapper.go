package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// PayloadMapper converts internal AssetData to Qdrant schema.Point representations.
// It is the SINGLE place where vector names, payload fields, and embedding
// channel mapping are configured — no hardcoded names anywhere else.
type PayloadMapper struct {
	store           AssetStore
	log             *zap.Logger
	searchTextBuild appsearchtext.SearchTextBuilder // optional; nil → fall back to asset.SearchText
}

// NewPayloadMapper creates a PayloadMapper. The SearchTextBuilder is
// optional — when nil (e.g. admin tooling, tests that don't need BM25),
// the mapper falls back to asset.SearchText (the pre-existing DB column).
// Composition root wires the canonical Registry via SetSearchTextBuilder.
func NewPayloadMapper(store AssetStore, log *zap.Logger) *PayloadMapper {
	return &PayloadMapper{
		store: store,
		log:   log,
	}
}

// SetSearchTextBuilder wires the canonical SearchTextBuilder port. When
// non-nil, assetToIndexDocumentNoValidate / AssetToIndexDocument delegate
// BM25 search-text construction to the builder (per-source strategies:
// youtube, artlist, voiceover, image, generated_image). When the builder
// returns empty for an input, the mapper falls back to asset.SearchText
// (the SQL column) for that asset. When the builder itself is nil, the
// mapper uses asset.SearchText directly.
func (m *PayloadMapper) SetSearchTextBuilder(b appsearchtext.SearchTextBuilder) {
	m.searchTextBuild = b
}

// FetchAsset delegates to the AssetStore.
func (m *PayloadMapper) FetchAsset(ctx context.Context, assetID string) (*AssetData, error) {
	return m.store.FetchAsset(ctx, assetID)
}

// ListAllAssetIDs delegates to the AssetStore.
func (m *PayloadMapper) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return m.store.ListAllAssetIDs(ctx)
}

// FetchAssetBatch delegates to the AssetStore (HIGH #8, July 2026).
func (m *PayloadMapper) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error) {
	return m.store.FetchAssetBatch(ctx, afterID, limit)
}

// AssetToPoint converts an AssetData to a Qdrant schema.Point using the manifest.
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
//   - QDRANT-001: the point ID is canonicalised via schema.AssetIDToQdrantPointID
//     (UUID v5 SHA-1 with project-namespacing). The canonical asset_id is
//     read from point.Payload["asset_id"] when a point is retrieved.
//   - PR 6 verdict §7.4 footer: drive_link / local_path / status are
//     forbidden locator keys; the IndexDocument airlock strips them
//     from the wire.
//
// QDRANT-001 (June 2026): this is still the only legal site that derives a
// Qdrant point ID from a media_assets.id (via the canonical
// schema.AssetIDToQdrantPointID call inside IndexDocumentToPoint).
func (m *PayloadMapper) AssetToPoint(ctx context.Context, asset *AssetData, schema *schema.IndexSchema) (*schema.Point, error) {
	doc, err := m.AssetToIndexDocument(ctx, asset, schema)
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
// DEPRECATED (July 2026): zero production consumers — all callers route
// through AssetToPoint → AssetToIndexDocument → IndexDocumentToPoint
// (the canonical path with vector validation + SearchTextBuilder).
// Retained for test backwards-compatibility only. Remove after 2026-08-15
// if no new consumers emerge.
//
// PR 6 (refactor/qdrant-index-document): BuildPayload now delegates to
// BuildPayloadFromDocument via the IndexDocument airlock so production
// writes satisfy verdict §11 (OBSERVED provenance). The wire shape is
// unchanged for legacy callers (assetToIndexDocumentNoValidate populates
// EmbeddingArtifacts whose ModelVersion defaults to the schema value;
// AssetData carries no separate observed source today).
//
// SearchTextBuilder integration: this is the package-level (no
// mapper receiver) entry point — it does NOT call into the
// SearchTextBuilder because it has no mapper receiver. This matches
// the pre-mapper era intent: callers that go through BuildPayload
// accept the asset.SearchText passthrough. Production callers go
// through AssetToIndexDocument which IS mapper-receiver-bound and
// DOES use the SearchTextBuilder.
//
// QDRANT-004 PR2 (June 2026): the canonical lifecycle key is
// `lifecycle_state` (SSOT — matches media_assets.lifecycle_state in SQLite,
// qdrant.schema.DefaultV3Schema().PayloadIndexes, and the search adapter
// filter). The previous `status` key was a legacy alias from pre-QDRANT-004
// ingest pipelines; it has been retired from the writer
// (BuildPayloadFromDocument below), the reader (search_adapter,
// clip_search_adapter), and the manifest (schema.DefaultV3Schema.PayloadIndexes).
// One-shot migration of historical points is the QDRANT-005B
// reconciler's repair path (target wave); until then legacy points carry
// both keys and the reader falls through silently.
//
// QDRANT-001 (June 2026) closure: drive_link is NEVER in the payload;
// the airlock strips it from AssetData.DriveLink → IndexDocument (no
// field there) → wire.
func BuildPayload(asset *AssetData, schema *schema.IndexSchema) map[string]interface{} {
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
// Task 4 (July 2026) — canonical dense-vector validation.
// ══════════════════════════════════════════════════════════════════════════

// channelPolicy classifies each dense vector channel as required,
// optional, or fatal-if-missing. The classification is per-channel
// because the embedding pipeline differs:
//
//   - text:       REQUIRED — every searchable asset must have a text embedding.
//   - transcript: OPTIONAL — YouTube-only; dropped when absent (PR 2).
//   - visual:     OPTIONAL — image/video assets only; dropped when absent.
//   - audio:      OPTIONAL — audio assets only; dropped when absent.
//   - any other:  OPTIONAL — future channels default to optional.
type channelPolicy int

const (
	policyRequired channelPolicy = iota // nil → ErrMissingRequiredVector
	policyOptional                      // nil → silently dropped
)

// classifyChannel returns the policy for the given Qdrant vector channel.
func classifyChannel(ch string) channelPolicy {
	switch ch {
	case "text":
		return policyRequired
	case "transcript", "visual", "audio":
		return policyOptional
	default:
		return policyOptional
	}
}

// validateDenseVector performs the canonical 5-step validation of a
// dense embedding vector before it is included in the IndexDocument.
//
// Checks (in order, first failure returned):
//  1. Nil check      → policyRequired → ErrMissingRequiredVector
//  2. Zero-length    → ErrEmptyVector
//  3. Dimension      → ErrVectorDimensionMismatch
//  4. NaN            → ErrNaNOrInf
//  5. Inf            → ErrNaNOrInf
//
// Returns nil when the vector is valid OR when it is nil AND the
// channel is optional (policyOptional → silent skip).
func validateDenseVector(channel string, vec []float32, expectedDim int, assetID string) error {
	// Step 1: nil check — required vs optional.
	if vec == nil {
		if classifyChannel(channel) == policyRequired {
			return &transport.ErrMissingRequiredVector{
				Channel: channel,
				AssetID: assetID,
			}
		}
		return nil // optional channel, absent is allowed
	}

	// Step 2: zero-length vector — present but corrupted.
	if len(vec) == 0 {
		return &transport.ErrEmptyVector{
			Channel: channel,
			AssetID: assetID,
		}
	}

	// Step 3: dimension mismatch.
	if len(vec) != expectedDim {
		return &transport.ErrVectorDimensionMismatch{
			Channel:  channel,
			Expected: expectedDim,
			Actual:   len(vec),
			AssetID:  assetID,
		}
	}

	// Step 4 & 5: NaN / Inf — reuse the canonical helper.
	for _, v := range vec {
		if isNaNOrInf(v) {
			return &transport.ErrNaNOrInf{
				Channel: channel,
				AssetID: assetID,
			}
		}
	}

	return nil
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
// in schema.SwitchReport.VersionMismatchPerChannel (PR 12).
//
// FORBIDDEN at this emitter: drive_link, local_path, status payload
// keys. The AirLock via IndexDocument strips these from the wire shape;
// this fn preserves the invariant. The freeze test in
// composition_test.go::TestComposition_FrozenQdrantIndexDocumentCanonicalTypes
// + the wire-level test below pin both halves of the invariant.
func BuildPayloadFromDocument(doc *IndexDocument, schema *schema.IndexSchema) map[string]interface{} {
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
func assetToIndexDocumentNoValidate(asset *AssetData, schema *schema.IndexSchema) *IndexDocument {
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
		SearchText:     asset.SearchText, // legacy pass-through (BuildPayload entry); mapper receivers override via AssetToIndexDocument
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
	// server-side inference). schema.SparseSpec carries no ModelVersion or
	// PreprocessVer — those fields live on schema.EmbeddingSpec (dense) only.
	for _, spec := range schema.SparseVectors {
		channel := VectorChannel(spec.Channel)
		model := spec.Model
		if model == "" {
			model = "qdrant/bm25"
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

// metadataStringSlice extracts a string-slice-typed field from a parsed
// JSON metadata map (used for "tags" arrays and "detected_entities").
// Returns nil when absent, non-array, or contains non-string elements.
// Filters out empty/whitespace-only strings so downstream joinTags
// helpers behave correctly.
func metadataStringSlice(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		// Also handle []string as a defensive fallback.
		if ss, ok := raw.([]string); ok {
			out := make([]string, 0, len(ss))
			for _, s := range ss {
				if s != "" {
					out = append(out, s)
				}
			}
			if len(out) == 0 {
				return nil
			}
			return out
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildSearchTextInput maps an AssetData row to the canonical
// SearchTextInput that the per-source strategies consume. The mapping
// is intentionally permissive: every field on SearchTextInput is a
// zero-or-more-value contract; strategies only read the subset they
// document (per `internal/infrastructure/indexing/searchtext/strategies.go`).
//
// AssetData field priorities:
//   - Title         ← asset.Name (canonical human-readable label)
//   - Tags          ← asset.Tags (already-typed []string)
//   - Category      ← asset.Category
//   - Channel       ← asset.ChannelID (YouTube channel name)
//   - Language      ← asset.Language (BCP-47)
//   - Description   ← metadata_json.$.description (when parsed)
//   - Transcript    ← metadata_json.$.transcript ONLY (no asset.SearchText
//     fallback — search_text is the BM25 OUTPUT, never the
//     raw transcript; falling back would feed the assembled
//     search text back into the YouTube strategy's
//     transcript slot and double-count it).
//   - Caption       ← metadata_json.$.caption
//   - Prompt        ← metadata_json.$.prompt
//   - DetectedEntities ← metadata_json.$.detected_entities
//   - OriginProvider   ← metadata_json.$.origin_provider
//
// The function returns a SearchTextInput that ALWAYS carries the
// canonical identifiers (AssetID, Source, MediaType) so the registry
// can dispatch on Source even when the freetext bag is empty.
func buildSearchTextInput(asset *AssetData) appsearchtext.SearchTextInput {
	if asset == nil {
		return appsearchtext.SearchTextInput{}
	}
	parseMetadataJSON(asset)
	return appsearchtext.SearchTextInput{
		AssetID:          asset.ID,
		Source:           asset.Source,
		MediaType:        asset.MediaType,
		Title:            asset.Name,
		Description:      metadataString(asset.Metadata, "description"),
		Transcript:       metadataString(asset.Metadata, "transcript"),
		Prompt:           metadataString(asset.Metadata, "prompt"),
		Caption:          metadataString(asset.Metadata, "caption"),
		Tags:             asset.Tags,
		Category:         asset.Category,
		Language:         asset.Language,
		Channel:          asset.ChannelID,
		DetectedEntities: metadataStringSlice(asset.Metadata, "detected_entities"),
		OriginProvider:   metadataString(asset.Metadata, "origin_provider"),
	}
}

// resolveSearchText is the canonical call site for search-text
// construction at indexing time. It owns the precedence order:
//
//  1. SearchTextBuilder (when wired via SetSearchTextBuilder) — returns
//     the per-source canonical text. A nil error is treated as "ok"
//     even when Build returns empty (legitimate empty strategies).
//     A non-nil error is logged at DEBUG and falls through — we
//     never block the IndexDocument on a search-text builder
//     failure (godlike/07 no-fake-availability contract).
//  2. asset.SearchText — the DB-stored pre-built value. Always the
//     graceful fallback when the builder is absent OR returns empty.
//
// Always returns a non-nil string (possibly "") so callers can write
// doc.SearchText directly without nil-checks.
//
// IMPORTANT: returns asset.SearchText UNCHANGED when the builder is nil
// — this is the byte-for-byte contract the pre-existing
// TestAssetToPoint_SparseVector_HasServerSideShape and other tests
// (which call NewPayloadMapper(store, log) WITHOUT SetSearchTextBuilder)
// pin. Production code paths (NewRuntime) wire the registry; admin
// CLI / tests / fixtures that bypass NewRuntime see the legacy
// pass-through behavior.
func (m *PayloadMapper) resolveSearchText(ctx context.Context, asset *AssetData) string {
	if asset == nil {
		return ""
	}
	if m.searchTextBuild != nil {
		input := buildSearchTextInput(asset)
		text, err := m.searchTextBuild.Build(ctx, input)
		if err != nil {
			if m.log != nil {
				// godlike/07 no-fake-availability: surface strategy
				// failures at Warn so operator dashboards catch a
				// panicking or misconfigured strategy. The mapper
				// still degrades gracefully (falls through to
				// asset.SearchText) so a single broken strategy
				// does not block the IndexDocument, but the
				// degradation is visible.
				m.log.Warn("SearchTextBuilder.Build error; falling back to asset.SearchText",
					zap.String("asset_id", asset.ID),
					zap.Error(err))
			}
			// Fall through to asset.SearchText — never block
			// the IndexDocument on a search-text builder issue.
		} else if text != "" {
			return text
		}
	}
	return asset.SearchText
}

// AssetToIndexDocument is the canonical Mapper airlock (PR 6). Builds
// an IndexDocument from a SQL-fetch AssetData and validates the
// per-channel vector dimensions / NaN / Inf before the wire is
// constructed. Returns the same typed errors as AssetToPoint
// (transport.ErrEmptyVector / transport.ErrVectorDimensionMismatch / transport.ErrNaNOrInf) so the
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
func (m *PayloadMapper) AssetToIndexDocument(ctx context.Context, asset *AssetData, schema *schema.IndexSchema) (*IndexDocument, error) {
	if asset == nil {
		return nil, fmt.Errorf("asset is nil")
	}
	if asset.ID == "" {
		return nil, fmt.Errorf("asset ID must not be empty")
	}
	doc := assetToIndexDocumentNoValidate(asset, schema)
	// Route BM25 search-text through the canonical SearchTextBuilder
	// (registered via SetSearchTextBuilder at composition root). The
	// helper falls back to asset.SearchText when the builder is nil
	// or returns empty — the contract preserves the pre-existing DB
	// pre-build path for legacy rows.
	doc.SearchText = m.resolveSearchText(ctx, asset)
	for _, spec := range schema.DenseVectors {
		channel := VectorChannel(spec.Channel)
		vec := m.getVectorForChannel(asset, spec.Channel)

		// Task 4 (July 2026): route through the canonical 5-step
		// validation helper instead of inline ad-hoc checks.
		// validateDenseVector returns nil for:
		//   - valid vectors (all checks pass)
		//   - optional-channel nil vectors (silent skip)
		// Returns typed error for:
		//   - required-channel nil → ErrMissingRequiredVector
		//   - zero-length vector   → ErrEmptyVector
		//   - dimension mismatch   → ErrVectorDimensionMismatch
		//   - NaN/Inf             → ErrNaNOrInf
		if err := validateDenseVector(spec.Channel, vec, spec.Dimensions, asset.ID); err != nil {
			return nil, err
		}
		if vec == nil {
			continue // optional channel, absent is allowed
		}

		doc.Embeddings[channel] = EmbeddingArtifact{
			Channel:       channel,
			Values:        vec,
			Model:         spec.Model,
			ModelVersion:  spec.ModelVersion,
			PreprocessVer: spec.PreprocessVer,
			Dimensions:    spec.Dimensions,
			GeneratedAt:   time.Now(),
		}
	}
	return doc, nil
}

// IndexDocumentToPoint is the canonical wire-shaping layer (PR 6).
// Reads an IndexDocument (the airlock output) and emits the Qdrant
// schema.Point shape. Mirrors the validation + sparse-channel handling of
// AssetToPoint on the IndexDocument side. Callers that already have
// an IndexDocument skip the AssetToIndexDocument step.
//
// Sparse-channel wire shape: Qdrant requires `{text, model}` for
// server-side BM25 inference; the bm25_text artifact records the
// model name; SearchText comes from the document. Empty SearchText
// drops the channel (mirrors the existing AssetToPoint behavior for
// backward compatibility with the PR 2 BM25 contract).
func (m *PayloadMapper) IndexDocumentToPoint(doc *IndexDocument, idxSchema *schema.IndexSchema) (*schema.Point, error) {
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
	return &schema.Point{
		ID:      schema.AssetIDToQdrantPointID(doc.AssetID),
		Vectors: vectors,
		Payload: BuildPayloadFromDocument(doc, idxSchema),
	}, nil
}
