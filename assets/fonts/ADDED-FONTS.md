# Added runtime fonts

Google Fonts added from the official `google/fonts` repository:

- `font-google-sans` — `Google-Sans.ttf`
- `font-dm-sans` — existing `DM-Sans.ttf` (identical to the fetched official variable font)
- `font-changa-one` — `Changa-One.ttf`
- `font-dm-serif-display` and `font-dm-serif-display-italic`
- `font-sf-pro-display` and style-specific IDs — SF Pro Display static OTF files

The Google Fonts families include their OFL license files. SF Pro Display came from the user-provided Drive archive, which contained no license file; see `SF-Pro-Display-SOURCE.txt`.

The fonts are indexed in `internal/capabilities/overlays/font_assets.go` and `internal/platform/renderinggen/font_assets.go`. They are staged in both PipelineGen and Chronon3D asset bundles.
