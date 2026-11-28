// Package scriptgeneration — clip_only_overlay_background.go owns the
// clip_only final-job overlay-canvas decision.
//
// Final-job overlays are rendered as opaque replacement windows, so the
// overlay artifact itself must carry the picture that belongs under the card.
// In a clip_only run that picture is the source clip: using a stock editorial
// plate would replace the clip with unrelated footage for the whole overlay
// window (r114 rendered the 1991 date card over boxing stock instead of the
// Tyson clip). The request builder leaves OverlayBackground empty for the
// single-clip case; this projection fills it from the resolved clip, whose
// verified local path and SHA are already bound on the scene.
package clips

import (
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SingleClipOverlayBackground returns the only source clip of a clip_only
// final job as the overlay canvas background. It returns nil when the run is
// not a clip_only final job, when a scene has no verified clip, or when the
// scenes reference more than one clip (there is no single truthful global
// plate in that case).
func SingleClipOverlayBackground(req contract.GenerateRequest, result *contract.GenerateResult) *scriptpkg.OverlayBackgroundSpec {
	if !req.FinalJob || req.MediaMode != scriptpkg.MediaModeClipOnly || result == nil {
		return nil
	}
	var chosen *contract.ClipReference
	seen := make(map[string]struct{})
	for i := range result.Scenes {
		clip := OverlaySceneClip(&result.Scenes[i])
		if clip == nil {
			return nil
		}
		id := strings.TrimSpace(clip.ID)
		path := strings.TrimSpace(clip.Path)
		sha := strings.TrimSpace(clip.SHA256)
		if id == "" || path == "" || sha == "" {
			return nil
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			chosen = clip
		}
	}
	if chosen == nil || len(seen) != 1 {
		return nil
	}
	return &scriptpkg.OverlayBackgroundSpec{
		Kind:      "video",
		AssetID:   chosen.ID,
		LocalPath: chosen.Path,
		SHA256:    chosen.SHA256,
		MediaType: "video/mp4",
		Fit:       OverlayBackgroundPlateFit,
		Loop:      true,
	}
}

// SceneOverlayBackgrounds projects each verified source clip into a
// producer-local background map for isolated final-job overlay renders. A
// child render consumes only the plate matching its OverlayItem.SceneID.
// Multi-clip scenes are accepted only when the scene has an authoritative
// Clip pointer; a plural-only scene must have exactly one clip.
func SceneOverlayBackgrounds(req contract.GenerateRequest, result *contract.GenerateResult) map[string]*capmodel.OverlayBackground {
	if !req.FinalJob || req.MediaMode != scriptpkg.MediaModeClipOnly || result == nil {
		return nil
	}
	backgrounds := make(map[string]*capmodel.OverlayBackground, len(result.Scenes))
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		sceneID := strings.TrimSpace(scene.ID)
		clip := OverlaySceneClip(scene)
		if sceneID == "" || clip == nil {
			continue
		}
		assetID := strings.TrimSpace(clip.ID)
		localPath := strings.TrimSpace(clip.Path)
		sha256 := strings.TrimSpace(clip.SHA256)
		if assetID == "" || localPath == "" || sha256 == "" {
			continue
		}
		backgrounds[sceneID] = &capmodel.OverlayBackground{
			Kind: "video",
			AssetRefs: []capmodel.OverlayAssetRef{{
				AssetID: assetID, LocalPath: localPath, SHA256: sha256, MediaType: "video/mp4",
			}},
			Fit: OverlayBackgroundPlateFit, Loop: true,
		}
	}
	if len(backgrounds) == 0 {
		return nil
	}
	return backgrounds
}

// OverlaySceneClip returns the scene's main clip, falling back to the sole
// entry of Clips when a producer populated only the plural projection.
func OverlaySceneClip(scene *contract.Scene) *contract.ClipReference {
	if scene == nil {
		return nil
	}
	if scene.Clip != nil {
		return scene.Clip
	}
	if len(scene.Clips) == 1 {
		return scene.Clips[0]
	}
	return nil
}
