// Package adapters — processor_vidrush_image_telemetry.go: per-image telemetry
// for the VidRush materialization processor.
//
// A run that yields the wrong image is only debuggable if every selected image
// can be traced back to the provider, the query and the score that produced
// it. One structured line per selected image is the cheapest form of that
// trace; selection order is preserved so the log mirrors the binding order.
package adapters

import (
	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// logVidRushSelectedImages emits one debug line per selected image. It is a
// no-op for a nil logger or an empty selection and never mutates its input.
func logVidRushSelectedImages(log *zap.Logger, segmentID string, images []scriptpkg.SegmentAssetCandidate) {
	if log == nil {
		return
	}
	for index, image := range images {
		log.Debug("VidRush image selected",
			zap.String("segment_id", segmentID),
			zap.Int("position", index),
			zap.String("asset_id", image.AssetID),
			zap.String("provider", image.Provider),
			zap.String("query", image.Query),
			zap.Float64("score", image.Score),
			zap.String("perceptual_hash", image.PerceptualHash),
		)
	}
}
