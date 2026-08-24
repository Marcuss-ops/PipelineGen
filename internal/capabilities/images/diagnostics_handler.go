// Package images (api/images) — diagnostics_handler.go holds the
// GET /api/images/diagnostics handler. Reports local wiring state
// for the image generation and animation subsystems.
//
// Territory: operational/meta (neither generated nor retrieved —
// it reports on the wiring state of both).
package images

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// Diagnostics handles GET /api/images/diagnostics. Reports the
// local state of the image generation and animation wiring via
// the service-layer Diagnostics() method.
func (h *ImagesHandler) Diagnostics(c *gin.Context) {
	if h.service == nil {
		apiutil.InternalError(c, fmt.Errorf("image service not configured"))
		return
	}

	apiutil.OK(c, h.service.Diagnostics())
}
