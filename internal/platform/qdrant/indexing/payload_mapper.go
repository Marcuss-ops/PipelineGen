// Package indexing — payload_mapper.go: canonical PayloadMapper surface.
//
// PR-PAYLOAD-MAPPER-SPLIT (July 2026): decomposed the original 684-LoC
// monolithic payload_mapper.go into 4 single-purpose files per
// AGENTS.md Pattern 5:
//
//   - payload_mapper.go            — slim orchestrator: struct +
//     constructor + delegated methods +
//     AssetToPoint + BuildPayload
//   - payload_mapper_validation.go — vector validation: getVectorForChannel,
//     channelPolicy, classifyChannel,
//     validateDenseVector, isNaNOrInf
//   - payload_mapper_document.go   — IndexDocument + Payload:
//     BuildPayloadFromDocument,
//     assetToIndexDocumentNoValidate,
//     domainAssetLifecycle,
//     AssetToIndexDocument,
//     IndexDocumentToPoint
//   - payload_mapper_searchtext.go — search text: parseMetadataJSON,
//     buildSearchTextInput,
//     resolveSearchText
package indexing

import (
	"context"

	"go.uber.org/zap"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// PayloadMapper converts internal AssetData to Qdrant schema.Point representations.
// It is the SINGLE place where vector names, payload fields, and embedding
// channel mapping are configured — no hardcoded names anywhere else.
type PayloadMapper struct {
	store           AssetStore
	log             *zap.Logger
	searchTextBuild appsearchtext.SearchTextBuilder // optional; nil → fall back to asset.SearchText

	// indexLanguages is the comma-separated BCP-47 language codes
	// derived from cfg.Media.Multilingual.Languages (Enabled=true).
	// Injected into SearchTextInput.Additional["index_languages"] so the
	// youtubeStrategy can filter TextTracks by configured languages.
	indexLanguages string

	// textTrackQuerier fetches text tracks from the asset_text_tracks
	// table. When non-nil, resolveSearchText populates
	// SearchTextInput.TextTracks so the youtubeStrategy can
	// concatenate multilingual transcripts. nil → TextTracks stay
	// empty (backwards-compatible for admin CLI / tests).
	textTrackQuerier TextTrackQuerier
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

// TextTrackQuerier is the narrow port the PayloadMapper uses to fetch
// text tracks from asset_text_tracks at search-text construction time.
// The concrete *texttracks.TextTrackRepositorySQLite satisfies this via
// the domain-level asset.TextTrackRepository; we define a narrower
// interface here to avoid pulling the full domain port surface.
type TextTrackQuerier interface {
	ListByAsset(ctx context.Context, assetID string) ([]assetpkg.TextTrack, error)
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

// SetIndexLanguages stores the comma-separated BCP-47 language codes
// that the youtubeStrategy uses to filter TextTracks. Called from the
// composition root (buildQdrantDeps) after NewRuntime.
func (m *PayloadMapper) SetIndexLanguages(langs string) {
	m.indexLanguages = langs
}

// SetTextTrackQuerier wires the text-track querier port so
// resolveSearchText can populate SearchTextInput.TextTracks for
// multilingual search-text construction. nil → TextTracks stay
// empty (backwards-compatible for admin CLI / tests).
func (m *PayloadMapper) SetTextTrackQuerier(q TextTrackQuerier) {
	m.textTrackQuerier = q
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
func BuildPayload(asset *AssetData, schema *schema.IndexSchema) map[string]any {
	return BuildPayloadFromDocument(assetToIndexDocumentNoValidate(asset, schema), schema)
}
