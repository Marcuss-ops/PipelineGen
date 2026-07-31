// Package indexing — index_airlock_commit.go: AssetData → IndexDocument airlock
// — COMMIT phase.
//
// Split rationale (acquire/commit/release), see index_airlock.go header:
//
//   - This file owns the `commit` phase: the production airlock method
//     `(*PayloadMapper).AssetToIndexDocument`.
//
// The commit phase runs AFTER the acquire phase (which produced an
// IndexDocument shell with empty EmbeddingArtifact.Values) and AFTER
// the release / canonicalisation pass (helpers in
// index_airlock_release.go have already polished raw strings). At this
// stage the airlock:
//
//  1. Re-runs assetToIndexDocumentNoValidate (acquire) to materialise
//     a fresh shell (it's cheap — the bulk is field-mapping).
//  2. Routes the BM25 search text through the canonical
//     SearchTextBuilder (fallback to asset.SearchText).
//  3. For each dense channel spec:
//     a. Resolves the per-channel vector from AssetData (the
//     getVectorForChannel receiver method on PayloadMapper).
//     b. Resamples the `visual` channel if dimension mismatch
//     (godlike/07 forward-prevention: rolling resample keeps
//     the airlock running across model-version drift).
//     c. Runs the canonical 5-step validateDenseVector check.
//     d. Populates EmbeddingArtifact with .Values + GeneratedAt
//     (the wire-ready stamp).
//
// Cross-file deps (same package `indexing`, accessed without explicit
// imports):
//   - assetToIndexDocumentNoValidate (acquire phase)
//   - validateDenseVector + resampleFloat32Vector (in
//     payload_mapper_document.go / payload_builder.go)
//   - m.resolveSearchText, m.getVectorForChannel (PayloadMapper
//     receiver methods)
//
// Wire-protocol invariant: the IndexDocument returned by commit has
// the same shape and field semantics as the legacy BuildPayload path;
// the only difference is the validation guarantees on dense channels
// (godlike/06/07 fail-closed contract).
package indexing

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// AssetToIndexDocument is the canonical Mapper airlock (PR 6). Builds
// an IndexDocument from a SQL-fetch AssetData and validates the
// per-channel vector dimensions / NaN / Inf before the wire is
// constructed. Returns the same typed errors as AssetToPoint
// (transport.ErrEmptyVector / transport.ErrVectorDimensionMismatch / transport.ErrNaNOrInf) so the
// upstream IndexWriter fail-closed at the type assertion already
// caught in BuildProcessBundle (`var _ clipindexer.VectorStoreIndexer
// = (*qdrant.IndexWriter)(nil)`) keeps behaving identically.
//
// The airlock strips Status / LocalPath at the IndexDocument boundary;
// DriveLink is intentionally retained inside IndexedMetadata as the
// canonical projection field, while its stale legacy metadata fallback
// is suppressed by the acquire phase. The wire-shape invariant is
// enforced statically (frozen test in
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
		if spec.Channel == "visual" && len(vec) > 0 && len(vec) != spec.Dimensions {
			if normalized, err := resampleFloat32Vector(vec, spec.Dimensions); err == nil {
				vec = normalized
			}
		}

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
