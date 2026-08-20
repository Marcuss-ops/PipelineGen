package script

// VideoRenderSpec is the opt-in video reconstruction contract carried by
// POST /api/script/generate. It is deliberately independent from the
// narration contract: generation decides the scenes, while the localized
// render fan-out materializes each selected clip.
type VideoRenderSpec struct {
	Enabled    bool                `json:"enabled,omitempty"`
	Watermark  *VideoWatermarkSpec `json:"watermark,omitempty"`
	Subtitles  *VideoSubtitlesSpec `json:"subtitles,omitempty"`
	OutputDir  string              `json:"output_dir,omitempty"`
	RequireGPU bool                `json:"require_gpu,omitempty"`
}

type VideoWatermarkSpec struct {
	Enabled  bool    `json:"enabled,omitempty"`
	Text     string  `json:"text,omitempty"`
	AssetID  string  `json:"asset_id,omitempty"`
	Position string  `json:"position,omitempty"`
	Opacity  float64 `json:"opacity,omitempty"`
	MarginPX int     `json:"margin_px,omitempty"`
}

type VideoSubtitlesSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"`
	StyleID string `json:"style_id,omitempty"`
}

// Normalize preserves the caller's explicit choices and enables the video
// path whenever either requested overlay is enabled. Empty values receive the
// same safe defaults as clip.render.
func (r *VideoRenderSpec) Normalize() {
	if r == nil {
		return
	}
	if r.Watermark != nil && r.Watermark.Enabled {
		r.Enabled = true
		if r.Watermark.Position == "" {
			r.Watermark.Position = "top_right"
		}
		if r.Watermark.Opacity == 0 {
			r.Watermark.Opacity = 1
		}
	}
	if r.Subtitles != nil && r.Subtitles.Enabled {
		r.Enabled = true
		if r.Subtitles.Mode == "" {
			r.Subtitles.Mode = "burn"
		}
	}
}
