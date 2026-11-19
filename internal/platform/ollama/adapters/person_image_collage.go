package adapters

import (
	"image"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// hasPanelGridCollage rejects source images with multiple near-full-length,
// straight panel boundaries. This pixel check complements the vision model:
// the runtime's current model has accepted a news montage as a single portrait.
// The implementation lives in the digest kernel so image verification and the
// person scorer reject the same assets.
func hasPanelGridCollage(data []byte) bool {
	return digest.HasPanelGridCollageBytes(data)
}

// hasPanelGridCollageImage is the decode-free half shared with the scorer,
// which decodes the verified bytes once and reuses the image for both the
// collage check and the VLM normalization.
func hasPanelGridCollageImage(img image.Image) bool {
	return digest.HasPanelGridCollageImage(img)
}
