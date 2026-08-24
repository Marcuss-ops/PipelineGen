// Package qdrantmm — qdrant_frame_indexer.go is the canonical
// concrete mediamemory.KeyframeVisualIndexer (Fase 4.1 — visual
// channel completion).
//
// godlike/06 SSOT (one canonical owner per fact): this adapter
// is the SOLE bridge between the mediamemory capability and
// pipelinegen_media_frames. Sister to qdrant_indexer.go (which
// owns the concept collection) — the two NEVER overlap.
//
// godlike/06 SSOT (collection boundary): keyframes live in their
// own collection (pipelinegen_media_frames) so the resolver hot
// path (phrase → concept → binding) is unaffected.
//
// godlike/07 NO-FAKE-AVAILABILITY: a missing transport / nil
// vector / wrong dimensionality surfaces as a typed sentinel
// (ErrSemanticNotConfigured / ErrLinkerEmbeddingFailed via %w).
// No silent zero-vector writes to the frame collection.
package qdrantmm

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	qdrantindexing "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
)

// FrameVectorDim is the canonical SigLIP so400m-patch14-384
// dimensionality for the visual channel. Mirrors
// schema.VisualEmbeddingDim (godlike/06 SSOT — same value
// declared once in qdrant/schema/schema.go); re-declared here
// for a fail-closed boundary at the mediamemory seam so a future
// schema bump cannot silently corrupt the frame collection.
const FrameVectorDim = 768

// FrameQdrantIndexer is the canonical mediamemory.KeyframeVisualIndexer
// backed by Qdrant.
type FrameQdrantIndexer struct {
	client *transport.Client
	writer qdrantindexing.ProjectionWriter
	log    *zap.Logger
}

// NewFrameQdrantIndexer constructs the canonical adapter. client
// MUST be non-nil (composition root fail-closed).
func NewFrameQdrantIndexer(client *transport.Client, log *zap.Logger) *FrameQdrantIndexer {
	return &FrameQdrantIndexer{client: client, writer: qdrantindexing.NewTransportProjectionWriter(client), log: log}
}

// Compile-time assertion: FrameQdrantIndexer satisfies
// mediamemory.KeyframeVisualIndexer. Drift surfaces as a build error.
var _ mediamemory.KeyframeVisualIndexer = (*FrameQdrantIndexer)(nil)

// IndexKeyframe writes a (video_id, ts_ms) frame point into
// pipelinegen_media_frames with the canonical visual-channel
// (768d SigLIP) vector.
//
// godlike/07 NO-FAKE-AVAILABILITY (fail-closed envelopes):
//
//   - nil client / nil vector / wrong dim → wrapped typed
//     sentinel; never silent zero-write.
//   - canonical point ID derivation (schema.FramePointID) is
//     INVARIANT under re-extract → Upsert is idempotent for
//     repeat (video_id, ts_ms) calls.
func (i *FrameQdrantIndexer) IndexKeyframe(
	ctx context.Context,
	videoID string,
	tsMs int64,
	assetID string,
	language string,
	vec []float32,
	model string,
) error {
	if i == nil || i.client == nil || i.writer == nil {
		return fmt.Errorf(
			"mediamemory: FrameQdrantIndexer not wired (client required): %w",
			mediamemory.ErrSemanticNotConfigured,
		)
	}
	if videoID == "" {
		return fmt.Errorf(
			"mediamemory: IndexKeyframe missing videoID: %w",
			mediamemory.ErrInvalidBindingInput,
		)
	}
	if len(vec) == 0 {
		return fmt.Errorf(
			"mediamemory: IndexKeyframe video=%q ts=%d: empty vector: %w",
			videoID, tsMs, mediamemory.ErrLinkerEmbeddingFailed,
		)
	}
	if len(vec) != FrameVectorDim {
		return fmt.Errorf(
			"mediamemory: IndexKeyframe video=%q ts=%d: %dd vector, expected %dd: %w",
			videoID, tsMs, len(vec), FrameVectorDim,
			mediamemory.ErrSemanticNotConfigured,
		)
	}

	pointID := schema.FramePointID(videoID, tsMs)
	if pointID == "" {
		return fmt.Errorf(
			"mediamemory: IndexKeyframe derived empty pointID for video=%q ts=%d: %w",
			videoID, tsMs, mediamemory.ErrInvalidBindingInput,
		)
	}

	embeddingVersion := schema.FrameEmbeddingVersion
	if model != "" {
		embeddingVersion = model
	}

	payload := map[string]any{
		"frame_id":          pointID,
		"video_id":          videoID,
		"asset_id":          assetID,
		"ts_ms":             tsMs,
		"language":          language,
		"source_provider":   "mediamemory.linker",
		"embedding_version": embeddingVersion,
	}

	point := schema.Point{
		ID:      pointID,
		Vectors: map[string]any{schema.FrameVectorName: vec},
		Payload: payload,
	}

	if err := i.writer.UpsertProjection(ctx, schema.FrameCollectionName, []schema.Point{point}); err != nil {
		return fmt.Errorf(
			"mediamemory: FrameQdrantIndexer upsert video=%q ts=%d: %w",
			videoID, tsMs, errors.Join(mediamemory.ErrLinkerEmbeddingFailed, err),
		)
	}
	return nil
}
