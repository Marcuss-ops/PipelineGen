package scripts

import (
	"strings"
)

// SceneImage is the legacy processor-output image record consumed
// by buildNormalizedScenes for the API response normalizer. The
// canonical PR 3 typed walk writes image binding data directly
// into model.SpecScene.Scenes[i].Bindings.Image, so SceneImage is
// no longer the canonical processor shape — it lives here because
// the response normalizer has its own slice shape (one per index).
//
// PR 3 (June 2026): the type moved here from pipeline_impl.go as
// part of the postprocessor_registry simplification. If the
// response normalizer is rewritten to walk SpecScene directly,
// this type can be deleted.
type SceneImage struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// buildNormalizedScenes merges clip scenes and generated scene images into a
// single response payload. The result keeps only the fields that actually have
// data:
//   - text
//   - image / images
//   - video / videos
//
// Empty scenes are dropped entirely so callers do not have to filter
// placeholder objects.
func buildNormalizedScenes(clipScenes []ClipScene, sceneImages []SceneImage) []map[string]any {
	maxLen := len(clipScenes)
	if len(sceneImages) > maxLen {
		maxLen = len(sceneImages)
	}
	if maxLen == 0 {
		return nil
	}

	scenes := make([]map[string]any, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		item := map[string]any{}

		if i < len(clipScenes) {
			if text := strings.TrimSpace(clipScenes[i].Text); text != "" {
				item["text"] = text
			}
			if link := strings.TrimSpace(clipScenes[i].DriveLink); link != "" {
				item["video"] = link
				item["videos"] = []string{link}
			}
		}

		if i < len(sceneImages) {
			if text := strings.TrimSpace(sceneImages[i].Text); text != "" {
				if _, exists := item["text"]; !exists {
					item["text"] = text
				}
			}
			if imageURL := strings.TrimSpace(sceneImages[i].URL); imageURL != "" {
				item["image"] = imageURL
				item["images"] = []string{imageURL}
			}
		}

		if len(item) > 0 {
			scenes = append(scenes, item)
		}
	}

	return scenes
}
