package scriptgeneration

// final_job_phrase_clip.go: the phrase-only scene clip handoff. A phrase-only
// scene has no source video, so the certified full-frame phrase animation IS
// the scene's visual. On the pure-manifest wire that animation lives inside
// the timeline artifact, so the scene clip is handed to the compositor as a
// TIMELINE WINDOW REFERENCE (item_id + in_us/out_us): the worker reads the
// phrase's frames from the same certified artifact the manifest places, and
// the phrase is never overlaid a second time.

import (
	"context"
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// finalJobPhraseOverlayClip projects the rendered full-frame phrase animation
// into the clip slot for a phrase-only scene. The returned asset references
// the phrase's window inside the certified timeline artifact; duration_ms is
// the window's certified duration.
func finalJobPhraseOverlayClip(ctx context.Context, result *GenerateResult, sceneID string) (map[string]any, int64, string, bool, error) {
	if result == nil || result.OverlayPlan == nil || result.OverlayRender == nil || result.OverlayRender.Manifest == nil {
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
	manifest := result.OverlayRender.Manifest
	entry, found := manifestEntryForItem(manifest, strings.TrimSpace(phrase.ID))
	if !found {
		return nil, 0, "", false, fmt.Errorf("final_job phrase-only scene %q has no manifest entry for phrase %q", sceneID, phrase.ID)
	}
	artifact := result.OverlayRender.Artifact
	if artifact == nil && len(result.OverlayRender.Batches) == 1 && result.OverlayRender.Batches[0].Artifact != nil {
		artifact = result.OverlayRender.Batches[0].Artifact
	}
	if artifact == nil {
		return nil, 0, "", false, fmt.Errorf("final_job phrase-only scene %q has no certified timeline artifact", sceneID)
	}
	sha := strings.TrimSpace(artifact.SHA256)
	asset, refErr := finalJobRenderArtifactRef(ctx, artifact)
	if refErr != nil || !artifact.CopyEligible || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.MimeType)), "video/") {
		return nil, 0, "", false, fmt.Errorf("final_job phrase artifact %q is not a certified downloadable video: %v", phrase.ID, refErr)
	}
	durationMS := (entry.OutUS - entry.InUS + 999) / 1000
	if durationMS <= 0 {
		return nil, 0, "", false, fmt.Errorf("final_job phrase artifact %q has no positive certified duration", phrase.ID)
	}
	asset["size_bytes"] = artifact.SizeBytes
	asset["duration_ms"] = durationMS
	asset["sha256"] = sha
	asset["timeline_item_id"] = entry.ItemID
	asset["in_us"] = entry.InUS
	asset["out_us"] = entry.OutUS
	return asset, durationMS, phrase.ID, true, nil
}

// manifestEntryForItem resolves one manifest entry by semantic item identity.
func manifestEntryForItem(manifest *capoverlay.OverlayManifest, itemID string) (capoverlay.OverlayManifestEntry, bool) {
	if manifest == nil {
		return capoverlay.OverlayManifestEntry{}, false
	}
	for _, entry := range manifest.Entries {
		if strings.TrimSpace(entry.ItemID) == itemID {
			return entry, true
		}
	}
	return capoverlay.OverlayManifestEntry{}, false
}
