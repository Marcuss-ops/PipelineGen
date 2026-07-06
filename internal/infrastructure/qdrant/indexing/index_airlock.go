// Package indexing — index_airlock.go: AssetData → IndexDocument airlock.
//
// Extracted from payload_mapper_document.go (July 2026).
// Owns: assetToIndexDocumentNoValidate, domainAssetLifecycle, AssetToIndexDocument.
package indexing

import (
	"context"
	"fmt"
	"time"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

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
			Title:        assetpkg.MetadataString(asset.Metadata, "title"),
			Description:  assetpkg.MetadataString(asset.Metadata, "description"),
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
func domainAssetLifecycle(raw string) assetpkg.LifecycleState {
	const fallback = "ACTIVE"
	if raw == "" {
		return assetpkg.LifecycleState(fallback)
	}
	return assetpkg.LifecycleState(raw)
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
