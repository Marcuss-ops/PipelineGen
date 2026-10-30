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
	if result == nil || result.OverlayPlan == nil || result.OverlayRender == nil {
		return nil, 0, "", false, nil
	}
	var phrase *capoverlay.OverlayItem
	for i := range result.OverlayPlan.Items {
		item := &result.OverlayPlan.Items[i]
		if item.SceneID == sceneID && finalJobPhraseVisualItem(*item) {
			if phrase != nil {
				return nil, 0, "", false, fmt.Errorf("final_job phrase-only scene %q has multiple rendered phrase visuals", sceneID)
			}
			phrase = item
		}
	}
	if phrase == nil {
		return nil, 0, "", false, nil
	}
	// A fused text chain has its own certified child render rather than one
	// manifest window. Reference those bytes directly through Objectstore; the
	// item is marked consumed so it is not emitted again as an overlay row.
	if strings.EqualFold(strings.TrimSpace(phrase.Kind), capoverlay.KindPhraseChain) {
		for _, rendered := range result.OverlayRender.Items {
			if strings.TrimSpace(rendered.ItemID) != strings.TrimSpace(phrase.ID) || rendered.Artifact == nil {
				continue
			}
			artifact := rendered.Artifact
			asset, refErr := finalJobRenderArtifactRef(ctx, artifact)
			if refErr != nil || !artifact.CopyEligible || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(artifact.MimeType)), "video/") {
				return nil, 0, "", false, fmt.Errorf("final_job phrase chain %q is not a certified downloadable video: %v", phrase.ID, refErr)
			}
			durationMS := (artifact.DurationUS + 999) / 1000
			if durationMS <= 0 {
				return nil, 0, "", false, fmt.Errorf("final_job phrase chain %q has no positive certified duration", phrase.ID)
			}
			asset["duration_ms"] = durationMS
			return asset, durationMS, phrase.ID, true, nil
		}
	}
	manifest := result.OverlayRender.Manifest
	if manifest == nil {
		return nil, 0, "", false, nil
	}
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
	// SubmitClip's strict Master DTO accepts source windows in milliseconds.
	// Keep the exact microsecond window in overlay_manifest; the clip slot
	// only needs the corresponding source trim to render the phrase visual.
	asset["start_ms"] = entry.InUS / 1000
	asset["end_ms"] = (entry.OutUS + 999) / 1000
	return asset, durationMS, phrase.ID, true, nil
}

func finalJobPhraseVisualItem(item capoverlay.OverlayItem) bool {
	if strings.EqualFold(strings.TrimSpace(item.Kind), "text_phrase") {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(item.Kind), capoverlay.KindPhraseChain) {
		return false
	}
	segments, ok := capoverlay.DecodeChainSegments(item.Params)
	if !ok || len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !strings.EqualFold(strings.TrimSpace(segment.Kind), "text_phrase") {
			return false
		}
	}
	return true
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
