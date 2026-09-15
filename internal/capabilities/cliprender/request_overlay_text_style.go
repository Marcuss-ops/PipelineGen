// Package cliprender — request_overlay_text_style.go owns the canonical
// default overlay text style applied when a caller requests an overlay without
// a full VideoVisualStyleSpec.
//
// Extracted 2026-09-15 from request.go to keep it under the godlike/08
// max_lines_per_file_strict cap (strict: fires above 600 lines). Same package,
// no behaviour change — the sibling-file split is the documented pattern for
// this gate.
package cliprender

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// defaultOverlayTextStyle builds the canonical readable-overlay defaults —
// Montserrat, white fill, black stroke, soft black shadow — scaled by the
// caller's requested size and weight. It is the single owner of the overlay
// text-style defaults so the normalization path never restates them.
func defaultOverlayTextStyle(fontSize, strokeWidth, shadowBlur float64) *scriptpkg.VideoVisualStyleSpec {
	return &scriptpkg.VideoVisualStyleSpec{
		Font:       "Montserrat",
		FontSizePX: fontSize,
		Color:      "#FFFFFF",
		Stroke: &scriptpkg.VideoStrokeSpec{
			Color: "#000000",
			Width: strokeWidth,
		},
		Shadow: &scriptpkg.VideoShadowSpec{
			Color:   "#000000",
			Opacity: 0.95,
			BlurPX:  shadowBlur,
			OffsetX: 2,
			OffsetY: 3,
		},
	}
}
