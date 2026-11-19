package adapters

import (
	"image"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// hasPanelGridCollageImage is the decode-free half shared with the scorer,
// which decodes the verified bytes once and reuses the image for both the
// collage check and the VLM normalization.
func hasPanelGridCollageImage(img image.Image) bool {
	return digest.HasPanelGridCollageImage(img)
}
