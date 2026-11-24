package overlayproject

import (
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// AnnotationsForLanguage returns only semantic annotations grounded in the
// requested language. The source annotation remains the compatibility surface
// for its own language; a translated render never reuses source-language spans
// when translated NLP is missing.
func AnnotationsForLanguage(scene contract.Scene, language contract.Language, sourceLanguage ...contract.Language) *scriptpkg.SceneAnnotations {
	if localized := scene.LocalizedAnnotations[language]; localized != nil {
		return localized
	}
	if scene.Annotations == nil {
		return nil
	}
	annotationLanguage := strings.TrimSpace(scene.Annotations.Language)
	if language == "" || strings.EqualFold(annotationLanguage, string(language)) {
		return scene.Annotations
	}
	// An unlabelled annotation is legacy source-language data. It may be used
	// for the source voiceover only; never project it onto a translated timing
	// stream, where the source entity surface can be absent or translated.
	if annotationLanguage == "" && len(sourceLanguage) > 0 && strings.EqualFold(string(sourceLanguage[0]), string(language)) {
		return scene.Annotations
	}
	return nil
}

// groupImageSFXByScene keeps declaration order for scenes while grouping the
// cue entries of the same scene. contract.Scene keys are compared case-insensitively,
// exactly like the planner's scene identity.
func groupImageSFXByScene(pins []scriptpkg.OverlayImageAsset) (map[string][]scriptpkg.OverlayImageSoundEffect, []string) {
	cues := make(map[string][]scriptpkg.OverlayImageSoundEffect)
	order := make([]string, 0)
	seen := make(map[string]struct{}, len(pins))
	for _, pin := range pins {
		if pin.SoundEffect == nil {
			continue
		}
		scene := strings.ToLower(strings.TrimSpace(pin.SceneID))
		if scene == "" {
			continue
		}
		if _, ok := seen[scene]; !ok {
			seen[scene] = struct{}{}
			order = append(order, scene)
		}
		cues[scene] = append(cues[scene], *pin.SoundEffect)
	}
	return cues, order
}

// ImageOverlayItemsBySceneWhere is the shared positional matcher: per scene,
// the image items that satisfy eligible, ordered by start time (then id).
func ImageOverlayItemsBySceneWhere(plan *capmodel.OverlayPlan, eligible func(*capmodel.OverlayItem) bool) map[string][]*capmodel.OverlayItem {
	out := make(map[string][]*capmodel.OverlayItem)
	for index := range plan.Items {
		item := &plan.Items[index]
		if !eligible(item) || !capoverlay.IsImageOverlayItem(*item) {
			continue
		}
		scene := strings.ToLower(strings.TrimSpace(item.SceneID))
		out[scene] = append(out[scene], item)
	}
	for scene := range out {
		items := out[scene]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].StartMs != items[j].StartMs {
				return items[i].StartMs < items[j].StartMs
			}
			return items[i].ID < items[j].ID
		})
	}
	return out
}
