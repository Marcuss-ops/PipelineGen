package scriptgeneration

import (
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// finalJobPhraseOverlayClip projects the already-rendered full-frame phrase
// animation into the clip slot for a phrase-only scene. Such scenes have no
// source video, so the normal localized-clip lane has nothing to render.
func finalJobPhraseOverlayClip(result *GenerateResult, sceneID string) (map[string]any, int64, string, bool, error) {
	if result == nil || result.OverlayPlan == nil || result.OverlayRender == nil {
		return nil, 0, "", false, nil
	}
	var phrase *capoverlay.OverlayItem
	for i := range result.OverlayPlan.Items {
		item := &result.OverlayPlan.Items[i]
		if item.SceneID == sceneID && item.Kind == "text_phrase" {
			if phrase != nil {
				return nil, 0, "", false, fmt.Errorf("final_job phrase-only scene %q has multiple rendered phrase items", sceneID)
			}
			phrase = item
		}
	}
	if phrase == nil {
		return nil, 0, "", false, nil
	}
	artifact, _, found := overlayArtifactForItem(result.OverlayRender, strings.TrimSpace(phrase.ID))
	if !found || artifact == nil {
		return nil, 0, "", false, fmt.Errorf("final_job phrase-only scene %q has no rendered phrase artifact", sceneID)
	}
	driveID := strings.TrimSpace(artifact.DriveFileID)
	sha := strings.TrimSpace(artifact.SHA256)
	if driveID == "" || len(sha) != 64 || artifact.SizeBytes <= 0 || !artifact.CopyEligible || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.MimeType)), "video/") {
		return nil, 0, "", false, fmt.Errorf("final_job phrase artifact %q is not a certified published video", phrase.ID)
	}
	durationMS := (artifact.DurationUS + 999) / 1000
	if durationMS <= 0 && artifact.FrameCount > 0 && artifact.FPSNum > 0 && artifact.FPSDen > 0 {
		numerator := int64(artifact.FrameCount) * 1000 * int64(artifact.FPSDen)
		durationMS = (numerator + int64(artifact.FPSNum) - 1) / int64(artifact.FPSNum)
	}
	if durationMS <= 0 {
		return nil, 0, "", false, fmt.Errorf("final_job phrase artifact %q has no positive certified duration", phrase.ID)
	}
	asset := map[string]any{
		"asset_id": firstFinalJobValue(artifact.ID, driveID), "drive_file_id": driveID,
		"url": driveFileWebLink(driveID), "sha256": sha,
		"size_bytes": artifact.SizeBytes, "duration_ms": durationMS,
	}
	return asset, durationMS, phrase.ID, true, nil
}
