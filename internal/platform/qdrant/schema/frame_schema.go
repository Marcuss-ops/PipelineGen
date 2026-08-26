// Package schema — frame_schema.go is the canonical Qdrant schema
// definition for the mediamemory `pipelinegen_media_frames`
// collection (Fase 4.1 — visual channel completion).
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SINGLE owner of the frame-index vector dimensions,
// channel name, and payload-index shape for keyframes extracted
// by the linker worker. The mediamemory capability NEVER
// redefines these values; it imports FrameIndexSchema() and uses
// IndexSchema's helpers.
//
// godlike/06 SSOT (collection boundary): keyframes live in their
// OWN collection rather than colliding with pipelinegen_media_concepts.
// Two reasons:
//
//   - Different entity: a keyframe is a (video_id, ts_ms)
//     timestamped visual; a concept is a phrase-derived entity.
//     Mixed collections force callers to switch on payload
//     discriminators at every read site, an anti-pattern.
//
//   - Different access pattern: the resolver hot path is
//     (phrase → concept → approved_binding) and does NOT need
//     keyframe vectors; the renderer is the only consumer that
//     benefits from frame vectors. Keeping separate collections
//     keeps the resolver hot path tight.
//
// godlike/07 NO-FAKE-AVAILABILITY: the schema lists every
// channel the linker writes. The Indexer concrete
// (qdrant_frame_indexer.go) is the consumer; the validator at
// boot determines "this collection is ready".
package schema

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// FrameCollectionName is the canonical physical collection name
// for media_frames (godlike/06 SSOT).
const FrameCollectionName = "pipelinegen_media_frames"

// FrameEmbeddingVersion is the canonical embedding schema version
// for frame vectors. It lives on IndexSchema.Version AND on each
// point's `embedding_version` payload field so readers can branch
// on the model release. Parallel of concept_schema's
// ConceptEmbeddingVersion SSOT; bumping is the same pattern
// (BumpEmbeddingVersion helper + admin reindex flow).
const FrameEmbeddingVersion = "v1"

// FrameVectorName is the canonical named-dense-vector channel for
// the visual embedding (SigLIP so400m-patch14-384 768d).
//
// godlike/06 SSOT (wire-channel vocabulary): this constant MUST
// match search.ChannelVisual byte-for-byte so the linker worker,
// the Qdrant schema, and the EmbeddingChannelRegistry agree on
// the same wire-channel name.
const FrameVectorName = "visual"

// FrameIndexSchema returns the canonical IndexSchema for
// pipelinegen_media_frames.
//
// Wire-channel vocabulary (godlike/06 SSOT; must match
// search.CanonicalChannelNames byte-for-byte):
//
//	dense "visual" : 768d Cosine, normalization on,
//	                   siglip-so400m-patch14-384
//
// Payload index shape:
//
//	frame_id          keyword : canonical frame point ID
//	video_id          keyword : canonical candidate/asset ID
//	asset_id          keyword : canonical media_assets.id
//	ts_ms             integer : canonical timestamp in ms
//	language          keyword : ISO 639-1 (it, en, ...)
//	source_provider   keyword : "artlist" / "youtube" / "images" / ...
//	embedding_version keyword : FrameEmbeddingVersion SSOT
func FrameIndexSchema() *IndexSchema {
	return &IndexSchema{
		Version:      FrameEmbeddingVersion,
		PhysicalName: FrameCollectionName,
		RuntimeAlias: FrameCollectionName,
		DenseVectors: []EmbeddingSpec{
			{
				Channel:      FrameVectorName,
				Model:        models.SigLIP.ID,
				ModelVersion: models.SigLIP.Revision,
				Dimensions:   models.SigLIP.Dimensions,
				Distance:     "Cosine",
				Normalized:   true,
			},
		},
		PayloadIndexes: []PayloadIndexSpec{
			{FieldName: "frame_id", FieldType: "keyword"},
			{FieldName: "video_id", FieldType: "keyword"},
			{FieldName: "asset_id", FieldType: "keyword"},
			{FieldName: "ts_ms", FieldType: "integer"},
			{FieldName: "language", FieldType: "keyword"},
			{FieldName: "source_provider", FieldType: "keyword"},
			{FieldName: "embedding_version", FieldType: "keyword"},
		},
	}
}

// FramePointIDPrefix is the canonical Qdrant point-id prefix for
// frame points. Parallel of conceptPointIDPrefix in
// adapters/qdrant_indexer.go.
//
// godlike/06 SSOT (deterministic point IDs): the canonical ID
// derivation for a frame is `frame-{videoID}-{tsMs}` so a
// re-extract of the same video at the same timestamp produces
// the SAME point (Upsert is canonical idempotent at the SQL/Qdrant
// layer). Hash-stable IDs also let the admin reindex loop skip
// already-indexed frames.
const FramePointIDPrefix = "frame-"

// FramePointID is the canonical Qdrant point-ID derivation for a
// (video_id, ts_ms) pair. Empty inputs return "" so the caller
// (FrameQdrantIndexer) can validate non-emptiness — no silent
// substitution of a zero-uuid here.
//
// godlike/06 SSOT (deterministic + Qdrant-compatible): the
// derivation is a SHA-256 of "videoID|tsMs" producing a UUID v8
// per the canonical pattern in pointid.go. Re-extraction of the
// same timestamp writes the same point so admin reindex loops
// are idempotent.
func FramePointID(videoID string, tsMs int64) string {
	if videoID == "" {
		return ""
	}
	h := sha256Sum([]byte(fmt.Sprintf("%s|%d", videoID, tsMs)))
	var b [16]byte
	copy(b[:], h[:16])
	b[6] = (b[6] & 0x0f) | 0x80 // RFC 9562 v8 version nibble
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant bits
	return FramePointIDPrefix + uuidV8String(b)
}
