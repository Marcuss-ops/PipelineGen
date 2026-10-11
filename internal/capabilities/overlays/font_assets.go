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
	// CanonicalInterFontPath is the optional payload-selected Inter family.
	CanonicalInterFontPath                 = "assets/fonts/Inter.ttf"
	CanonicalPlayfairDisplayItalicFontPath = "assets/fonts/PlayfairDisplay-Italic.ttf"
	// CanonicalInterBoldFontPath is the payload-selected Inter Bold family.
	CanonicalInterBoldFontPath = "assets/fonts/Inter-Bold.ttf"
	// CanonicalBricolageGrotesqueFontPath is the payload-selected Bricolage family.
	CanonicalBricolageGrotesqueFontPath = "assets/fonts/Bricolage-Grotesque.ttf"
	CanonicalGoogleSansFontPath         = "assets/fonts/Google-Sans.ttf"
	CanonicalChangaOneFontPath          = "assets/fonts/Changa-One.ttf"
	CanonicalDMSerifDisplayFontPath     = "assets/fonts/DM-Serif-Display.ttf"
	// GoldenFontHash is the SHA-256 of CanonicalTextFontPath.
	GoldenFontHash = "690243adfefe0ce154b547db6205794bd30ac4277275179517a90994f4980648"
	// GoldenPresetFontHash is the SHA-256 of CanonicalPresetFontPath.
	GoldenPresetFontHash = "983676516167748b74de6f4771fb384c664fd913acb8b471122ecacf5da5ea6c"
	// CanonicalInterFontHash is the SHA-256 of CanonicalInterFontPath.
	CanonicalInterFontHash                 = "29160a80ff49ddcab2c97711247e08b1fab27a484a329ce8b813d820dc559031"
	CanonicalPlayfairDisplayItalicFontHash = "a5e26dc5e2e77fb2803a0bf02fd4f81ee136ec8dea863ccdb0c59a263b21378b"
	// CanonicalInterBoldFontHash is the SHA-256 of CanonicalInterBoldFontPath.
	CanonicalInterBoldFontHash = "deb990e241954e1af87c6033d40a76cf0f48e0d112541794526051ac4a3ee2c8"
	// CanonicalBricolageGrotesqueFontHash is the SHA-256 of CanonicalBricolageGrotesqueFontPath.
	CanonicalBricolageGrotesqueFontHash = "413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682"
	// CanonicalOswaldFontPath is the payload-selected Oswald family.
	CanonicalOswaldFontPath = "assets/fonts/Oswald.ttf"
	// CanonicalOswaldFontHash is the SHA-256 of CanonicalOswaldFontPath.
	CanonicalOswaldFontHash = "5b38c246e255a12f5712d640d56bcced0472466fc68983d2d0410ec0457c2817"
	// CanonicalAbrilFatfaceFontPath is the payload-selected Abril Fatface family.
	CanonicalAbrilFatfaceFontPath = "assets/fonts/AbrilFatface-Regular.ttf"
	// CanonicalAbrilFatfaceFontHash is the SHA-256 of CanonicalAbrilFatfaceFontPath.
	CanonicalAbrilFatfaceFontHash = "5971d4a3758a922a9fedc7f6fb825a96341a2e718c45a4b2c9a6b417c8c4dbe9"
)

// CanonicalFontAsset binds a payload-selectable font family ID to the exact
// workspace path and content hash the render queue stages. This map is the
// single source of truth for which families the queue producer can stage:
// SupportedFontFamilies, runtimeFontAssets and the RenderingGen bundle gate
// must all agree with it (pinned by TestCanonicalFontAssetsParity), so adding
// a family is one map entry, never a switch case in three files.
type CanonicalFontAsset struct {
	Path   string
	SHA256 string
}

// CanonicalFontAssetsByFamily owns every payload-selectable font family.
// Keys are the exact IDs accepted by OverlayStyleSpec.FontFamily.
var CanonicalFontAssetsByFamily = map[string]CanonicalFontAsset{
	"poppins":                 {Path: CanonicalPresetFontPath, SHA256: GoldenPresetFontHash},
	"inter":                   {Path: CanonicalInterFontPath, SHA256: CanonicalInterFontHash},
	"inter_bold":              {Path: CanonicalInterBoldFontPath, SHA256: CanonicalInterBoldFontHash},
	"dejavu_sans":             {Path: CanonicalTextFontPath, SHA256: GoldenFontHash},
	"playfair_display_italic": {Path: CanonicalPlayfairDisplayItalicFontPath, SHA256: CanonicalPlayfairDisplayItalicFontHash},
	"bricolage_grotesque":     {Path: CanonicalBricolageGrotesqueFontPath, SHA256: CanonicalBricolageGrotesqueFontHash},
	"oswald":                  {Path: CanonicalOswaldFontPath, SHA256: CanonicalOswaldFontHash},
	"abril_fatface":           {Path: CanonicalAbrilFatfaceFontPath, SHA256: CanonicalAbrilFatfaceFontHash},
}

// ModernFontPaths is the local, workspace-relative registry for the
// downloaded Google Fonts variable-font fleet. RenderingGen resolves these
// paths through ResolveFontAsset, so they share its absolute-path and
// SHA-256 contract instead of being loaded ad hoc.
var ModernFontPaths = map[string]string{
	"font-inter":                            "assets/fonts/Inter.ttf",
	"font-manrope":                          "assets/fonts/Manrope.ttf",
	"font-dm-sans":                          "assets/fonts/DM-Sans.ttf",
	"font-instrument-sans":                  "assets/fonts/Instrument-Sans.ttf",
	"font-plus-jakarta-sans":                "assets/fonts/Plus-Jakarta-Sans.ttf",
	"font-sora":                             "assets/fonts/Sora.ttf",
	"font-space-grotesk":                    "assets/fonts/Space-Grotesk.ttf",
	"font-outfit":                           "assets/fonts/Outfit.ttf",
	"font-urbanist":                         "assets/fonts/Urbanist.ttf",
	"font-bricolage-grotesque":              "assets/fonts/Bricolage-Grotesque.ttf",
	"font-playfair-display-italic":          "assets/fonts/PlayfairDisplay-Italic.ttf",
	"font-google-sans":                      "assets/fonts/Google-Sans.ttf",
	"font-changa-one":                       "assets/fonts/Changa-One.ttf",
	"font-dm-serif-display":                 "assets/fonts/DM-Serif-Display.ttf",
	"font-dm-serif-display-italic":          "assets/fonts/DM-Serif-Display-Italic.ttf",
	"font-sf-pro-display":                   "assets/fonts/SF-Pro-Display-Regular.otf",
	"font-sf-pro-display-ultralight":        "assets/fonts/SF-Pro-Display-Ultralight.otf",
	"font-sf-pro-display-ultralight-italic": "assets/fonts/SF-Pro-Display-UltralightItalic.otf",
	"font-sf-pro-display-thin":              "assets/fonts/SF-Pro-Display-Thin.otf",
	"font-sf-pro-display-thin-italic":       "assets/fonts/SF-Pro-Display-ThinItalic.otf",
	"font-sf-pro-display-light":             "assets/fonts/SF-Pro-Display-Light.otf",
	"font-sf-pro-display-light-italic":      "assets/fonts/SF-Pro-Display-LightItalic.otf",
	"font-sf-pro-display-regular-italic":    "assets/fonts/SF-Pro-Display-RegularItalic.otf",
	"font-sf-pro-display-medium":            "assets/fonts/SF-Pro-Display-Medium.otf",
	"font-sf-pro-display-medium-italic":     "assets/fonts/SF-Pro-Display-MediumItalic.otf",
	"font-sf-pro-display-semibold":          "assets/fonts/SF-Pro-Display-Semibold.otf",
	"font-sf-pro-display-semibold-italic":   "assets/fonts/SF-Pro-Display-SemiboldItalic.otf",
	"font-sf-pro-display-bold":              "assets/fonts/SF-Pro-Display-Bold.otf",
	"font-sf-pro-display-bold-italic":       "assets/fonts/SF-Pro-Display-BoldItalic.otf",
	"font-sf-pro-display-heavy":             "assets/fonts/SF-Pro-Display-Heavy.otf",
	"font-sf-pro-display-heavy-italic":      "assets/fonts/SF-Pro-Display-HeavyItalic.otf",
	"font-sf-pro-display-black":             "assets/fonts/SF-Pro-Display-Black.otf",
	"font-sf-pro-display-black-italic":      "assets/fonts/SF-Pro-Display-BlackItalic.otf",
}
