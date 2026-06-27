package scripts

import (
	"strings"
)

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
