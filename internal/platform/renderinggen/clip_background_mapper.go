// Package renderinggen — clip_background_mapper.go
//
// The background half of the clip-plan → overlay-plan mapping. It lives in its
// own file because the background is the one block whose SHAPE is a renderer
// decision — which layer type samples the plate (image vs video), which suffix
// the plate is staged under, and which fit crops it — and keeping that decision
// in one cohesive unit is what stops it drifting back to a hardcoded
// "video"/"background.mp4" pair. clip_plan_mapper.go owns the plan as a whole
// and calls mapBackground once.
package renderinggen

import (
	"fmt"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// Background wire vocabulary. These are the renderer's own literals (its
// overlay contract declares kind ∈ none|color|image|video and fit ∈
// cover|contain|stretch|none), restated here as the values this mapper is
// allowed to emit — a mapper-side typo would otherwise be accepted by the
// decoder and silently defaulted.
const (
	backgroundKindVideo = "video"
	backgroundFitCover  = "cover"
)

// mapBackground projects the sealed plan's background block onto the wire.
//
// It returns nil for mode=none (no layer emitted) and a typed error for a mode
// or kind the renderer cannot honour — never a partially-described layer, which
// is what made an image plate render as a video source before.
func mapBackground(plan cliprender.ClipRenderPlanV1) (*overlayBackground, error) {
	if plan.Background == nil {
		return nil, nil
	}
	switch plan.Background.Mode {
	case cliprender.BackgroundModeNone:
		return nil, nil
	case cliprender.BackgroundModeBlurSource:
		// blur_source is not a first-class overlay-plan primitive. It is
		// expressed as a "video" background referencing the source asset with
		// the ONE fit the renderer actually honours (cover): the historical
		// "blur_cover" was not a FitMode of the render contract
		// (cover|contain|stretch|none) and was silently degraded to cover at
		// the boundary — an unblurred plate with no error anywhere. The fit is
		// now explicit and honoured; the blur itself needs a real compositor
		// effect, which the plan contract does not carry yet.
		return &overlayBackground{
			Kind: backgroundKindVideo,
			AssetRefs: []overlayAssetRef{{
				AssetID:   plan.Source.AssetID,
				SHA256:    plan.Source.SHA256,
				URL:       hashAddressedPath(plan.Source.AssetID, "source.mp4"),
				MediaType: "video/mp4",
			}},
			Fit:  backgroundFitCover,
			Loop: true,
		}, nil
	case cliprender.BackgroundModeAsset:
		filename, err := backgroundStagedFilename(plan)
		if err != nil {
			return nil, err
		}
		// The kind is the sealed plan's resolved media family (image | video) —
		// never inferred from a filename here. It selects the renderer's layer
		// type verbatim.
		return &overlayBackground{
			Kind: plan.Background.Kind,
			AssetRefs: []overlayAssetRef{{
				AssetID:   plan.Background.AssetID,
				SHA256:    plan.Background.SHA256,
				URL:       hashAddressedPath(plan.Background.AssetID, filename),
				MediaType: backgroundMediaType(plan.Background.Kind),
			}},
			// cover is the deterministic crop-to-fill the background contract
			// promises for both families (an image plate and a video plate are
			// cropped identically), so a 4:3 plate and a 21:9 plate produce the
			// same framing decision on every host.
			Fit:  backgroundFitCover,
			Loop: plan.Background.Kind == cliprender.BackgroundKindVideo,
		}, nil
	default:
		return nil, fmt.Errorf("clip plan mapper: unsupported background mode %q", plan.Background.Mode)
	}
}

// backgroundStagedFilename is the SINGLE owner of the logical filename a
// background plate is staged under. Both the plan emission and the asset
// prefetch read it, so the URL the plan references is exactly the object the
// queue uploads.
//
// The suffix comes from the materialized asset's own name (the CAS keeps the
// origin suffix) whenever it is compatible with the declared kind, so the
// worker's asset store sees the real container (a JPEG plate must not be staged
// as .png). When the local name carries no usable suffix the canonical one for
// the kind is used. A suffix that CONTRADICTS the kind is a compile error: it
// means the plan resolved an asset whose bytes are not the media family the
// render layer will sample.
func backgroundStagedFilename(plan cliprender.ClipRenderPlanV1) (string, error) {
	if plan.Background == nil {
		return "", fmt.Errorf("clip plan mapper: background staged filename requires a resolved background block")
	}
	fallback := ""
	switch plan.Background.Kind {
	case cliprender.BackgroundKindImage:
		fallback = "background.png"
	case cliprender.BackgroundKindVideo:
		fallback = "background.mp4"
	default:
		return "", fmt.Errorf("clip plan mapper: unsupported background kind %q", plan.Background.Kind)
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(plan.Background.Path)))
	if ext == "" {
		return fallback, nil
	}
	allowed := backgroundImageExtensions
	if plan.Background.Kind == cliprender.BackgroundKindVideo {
		allowed = backgroundVideoExtensions
	}
	if _, ok := allowed[ext]; !ok {
		return "", fmt.Errorf("clip plan mapper: background kind=%s does not accept a %q plate (materialized %q)", plan.Background.Kind, ext, plan.Background.Path)
	}
	return "background" + ext, nil
}

// backgroundMediaType is the declared media type of the staged background
// plate. It is what the asset registry falls back to when a URL carries no
// suffix, so it must agree with the sealed plan's kind.
func backgroundMediaType(kind string) string {
	if kind == cliprender.BackgroundKindImage {
		return "image/png"
	}
	return "video/mp4"
}

// The suffixes each background family accepts. Listing them (instead of
// trusting "starts with image/") keeps the check honest: a .gif is a still
// plate the image renderer accepts, a .m4v is a video container.
var backgroundImageExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".webp": {}, ".bmp": {}, ".gif": {}, ".avif": {},
}

var backgroundVideoExtensions = map[string]struct{}{
	".mp4": {}, ".m4v": {}, ".mov": {}, ".mkv": {}, ".webm": {}, ".avi": {},
}
