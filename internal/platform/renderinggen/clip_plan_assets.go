// Package renderinggen — clip_plan_assets.go
//
// The content-addressed asset side of the clip-plan mapping: the identity of an
// overlay segment, and the prefetch list the queue stages before a job is ever
// claimed. It lives apart from clip_plan_mapper.go because these two functions
// are the contract between the plan the worker reads and the objects the object
// store actually holds — they must agree byte for byte, and that agreement is
// easier to keep in one small file than at the bottom of a 700-line mapper.
package renderinggen

import (
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// overlaySegmentAssetID is the SINGLE owner of the overlay segment's
// content-addressed asset identity. The mapper (plan emission) and the asset
// prefetch (object-store staging) both derive the logical path from it, so the
// URL the plan references is exactly the object the queue materializes.
func overlaySegmentAssetID(segment *cliprender.PlanOverlaySegment) string {
	short := strings.ToLower(strings.TrimSpace(segment.SHA256))
	if len(short) > 16 {
		short = short[:16]
	}
	if short == "" {
		// Fail-safe: the plan validator requires a sha256, so this is only
		// reachable from a hand-built plan; keep the id content-independent.
		short = "segment"
	}
	return "overlay-" + short
}

// overlaySegmentItemID makes a semantic ITEM identity unique per declared
// segment. Two segments can legitimately share content (the same item rendered
// once, composited on two windows), so the content-addressed asset id alone
// would collide: item ids are the Chronon layer ids, which must be unique.
func overlaySegmentItemID(assetID string, index int) string {
	return fmt.Sprintf("%s-%d", assetID, index)
}

// overlayPlanAssets returns the content-addressed AssetRef list corresponding
// to a ClipRenderPlanV1. These refs use the same hash-addressed logical paths
// as the serialised overlay plan so the worker can materialise each asset from
// the object store.
func overlayPlanAssets(plan cliprender.ClipRenderPlanV1) ([]assetRef, error) {
	refs := []assetRef{{
		Hash:        plan.Source.SHA256,
		LogicalPath: hashAddressedPath(plan.Source.AssetID, "source.mp4"),
	}}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		// The staged name must match the wire URL byte for byte: a mismatch
		// leaves the worker with a plan entry pointing at an object the queue
		// never uploaded (a render that fails after compilation).
		filename, err := backgroundStagedFilename(plan)
		if err != nil {
			return nil, err
		}
		refs = append(refs, assetRef{
			Hash:        plan.Background.SHA256,
			LogicalPath: hashAddressedPath(plan.Background.AssetID, filename),
		})
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeBlurSource {
		// blur_source reuses the source asset — already registered above.
	}
	if plan.Subtitles != nil {
		refs = append(refs, assetRef{
			Hash:        plan.Subtitles.SHA256,
			LogicalPath: hashAddressedPath(plan.Subtitles.SHA256, "subtitles.ass"),
		})
		// A burn-in subtitle plan must ship a materialised font. RenderingGen
		// resolves the subtitle glyphs from the first .ttf/.otf in the job's
		// asset list; previously only Poppins was added here, while the
		// production default style is Montserrat, causing the worker to fail
		// after compilation with "requires a materialized font".
		if plan.Subtitles.Mode == cliprender.SubtitlesModeBurn {
			fontLoader := watermarkFontAsset
			if plan.Subtitles.Style != nil &&
				strings.Contains(strings.ToLower(strings.TrimSpace(plan.Subtitles.Style.Font)), "poppins") {
				fontLoader = poppinsFontAsset
			}
			font, err := fontLoader()
			if err != nil {
				return nil, fmt.Errorf("clip plan mapper: subtitle font: %w", err)
			}
			refs = append(refs, font)
		}
	}
	if plan.Watermark != nil && plan.Watermark.SHA256 != "" {
		refs = append(refs, assetRef{
			Hash:        plan.Watermark.SHA256,
			LogicalPath: hashAddressedPath(plan.Watermark.AssetID, "watermark.png"),
		})
	}
	if plan.Overlay != nil {
		// Every declared segment must be staged, not just the first: a segment
		// the worker cannot materialize fails the render after compilation.
		for _, segment := range plan.Overlay.Segments {
			refs = append(refs, assetRef{
				Hash:        segment.SHA256,
				LogicalPath: hashAddressedPath(overlaySegmentAssetID(&segment), "overlay.mp4"),
				LocalPath:   segment.Path,
			})
		}
	}
	if plan.Watermark != nil && plan.Watermark.Text != "" && plan.Watermark.SHA256 == "" {
		font, err := watermarkFontAssetForStyle(plan.Watermark.Style)
		if err != nil {
			return nil, fmt.Errorf("watermark font: %w", err)
		}
		refs = append(refs, font)
	}
	return refs, nil
}

// assetRef is a hash-addressed asset pointer used internally by this package.
type assetRef struct {
	Hash        string
	LogicalPath string
	LocalPath   string
}
