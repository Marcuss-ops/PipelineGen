package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// attachGroundedCaptionsToSceneImages preserves a grounded entity name on a
// scene image when VidRush resolved that entity to the same image bytes. The
// caption is linked by the content hash, so an unrelated scene entity can
// never be stamped onto the image.
func attachGroundedCaptionsToSceneImages(items []capabilityoverlay.OverlayItem, scenes []Scene, runID string) {
	type groundedCaption struct {
		name    string
		sceneID string
	}
	bySceneAndHash := make(map[string]groundedCaption)
	for _, scene := range scenes {
		if scene.Annotations == nil {
			continue
		}
		entities := append(append([]scriptpkg.AnnotatedEntity(nil), scene.Annotations.PrimaryEntities...), scene.Annotations.SecondaryEntities...)
		for _, entity := range entities {
			if entity.Image == nil {
				continue
			}
			name := strings.TrimSpace(entity.Text)
			if name == "" {
				name = strings.TrimSpace(entity.CanonicalName)
			}
			hash := strings.ToLower(strings.TrimSpace(entity.Image.SHA256))
			if name == "" || hash == "" {
				continue
			}
			key := scene.ID + "\x00" + hash
			if _, exists := bySceneAndHash[key]; !exists {
				bySceneAndHash[key] = groundedCaption{name: name, sceneID: scene.ID}
			}
		}
	}

	ordinal := 0
	for index := range items {
		item := &items[index]
		if item.Kind != "image" || strings.TrimSpace(item.EntityCaption) != "" {
			continue
		}
		var match groundedCaption
		for _, ref := range item.AssetRefs {
			for _, digest := range []string{ref.SHA256, ref.AssetID} {
				if caption, ok := bySceneAndHash[item.SceneID+"\x00"+strings.ToLower(strings.TrimSpace(digest))]; ok {
					match = caption
					break
				}
			}
			if match.name != "" {
				break
			}
		}
		if match.name == "" {
			continue
		}
		item.EntityCaption = match.name
		item.CaptionMotionID = capabilityoverlay.SelectEntityCaptionMotionAt(runID, match.sceneID, ordinal)
		ordinal++
	}
}
