package overlays

// Canonical font assets the queue producer stages into the render payload.
//
// RenderingGen's worker preflights the preset-owned font before applying a
// layer's explicit font_asset override, so the producer keeps both
// content-addressed fonts in the queue payload: the explicit PipelineGen font
// stays authoritative for the layer, while the preset font satisfies the
// registry dependency during plan preparation.
const (
	// CanonicalTextFontPath is the workspace-relative path of the canonical
	// PipelineGen text font.
	CanonicalTextFontPath = "assets/fonts/DejaVuSans.ttf"
	// CanonicalPresetFontPath is the workspace-relative path of the font the
	// Chronon visual presets reference.
	CanonicalPresetFontPath = "assets/fonts/Poppins-Bold.ttf"
	// GoldenFontHash is the SHA-256 of CanonicalTextFontPath.
	GoldenFontHash = "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648"
	// GoldenPresetFontHash is the SHA-256 of CanonicalPresetFontPath.
	GoldenPresetFontHash = "983676516167748b74de6f4771fb384c664fd913acb8b471122ecacf5da5ea6c"
)
