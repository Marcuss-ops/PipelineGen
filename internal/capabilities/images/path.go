package images

import imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"

// extractStyleFromPath is the temporary root compatibility seam.
// Canonical style-path ownership lives in images/styles.
func extractStyleFromPath(pathRel string) string {
	return imagestyles.ExtractFromPath(pathRel)
}
