// Package images (api/images) — generated_styles_handler.go holds
// the GET /api/images/generated/styles handler and the
// styleDefToInfo projection helper.
//
// Per the golden rule: styles are generated/AI territory — they
// belong under /generated/* but are a separate capability from
// search (read) and generate (write). Keeping them in their own
// file prevents the generated search handler from accidentally
// importing style logic.
package images

import (
	domain "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// ── GET /api/images/generated/styles ────────────────────────────────

// GeneratedStyles handles GET /api/images/generated/styles.
// Returns the canonical StyleRegistry contents. Each style
// is projected to a StyleInfo DTO so the canonical type's
// internal fields don't leak into the API surface.
func (h *ImagesHandler) GeneratedStyles(c *gin.Context) {
	reg := h.service.StylesRegistry()
	if reg == nil {
		apiutil.OK(c, StylesResponse{Styles: []StyleInfo{}, Count: 0})
		return
	}

	defs := reg.List()
	out := make([]StyleInfo, 0, len(defs))
	for _, d := range defs {
		out = append(out, styleDefToInfo(d))
	}
	apiutil.OK(c, StylesResponse{Styles: out, Count: len(out)})
}

// styleDefToInfo projects a canonical GenerationStyle to a StyleInfo
// DTO for the admin styles endpoint.
//
// The slim 8-field StyleDefinition is the source-of-truth for the
// projection here. The StyleID JSON wire format preserves the
// canonical case ("Cinematic" → "Cinematic") to stay byte-compatible
// with the pre-existing wire-format.
func styleDefToInfo(s domain.GenerationStyle) StyleInfo {
	return StyleInfo{
		StyleID:        s.Name,
		Name:           s.Name,
		Version:        int(s.Version),
		PromptSuffix:   s.PromptSuffix,
		NegativePrompt: s.NegativePrompt,
		DestinationKey: s.DestinationKey,
		Enabled:        s.Enabled,
	}
}
