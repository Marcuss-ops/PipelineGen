// Package overlays — editorial_bullet_style.go validates the per-bullet
// presentation pins of the script.generate payload before they reach the
// overlay compiler.
//
// The compiler applies a pin to the scene's projected editorial bullet. That
// bullet is the only item a scene may carry, so a pin cannot address the wrong
// item; what the payload can still get wrong is the identity itself (an empty
// or duplicated scene), a font family the runtime does not bundle, a size the
// renderer cannot honour, or a motion id the animation registry does not
// certify for a bullet. Each of those is rejected here, with the field named,
// instead of being dropped by the planner.
package overlays

import (
	"fmt"
	"math"
	"strings"

	capanimreg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	// MaxEditorialBulletStyles bounds the block: the planner budgets far fewer
	// bullets than this in one run, so a larger block is a producer bug.
	MaxEditorialBulletStyles = 64
	// EditorialBulletFontSizeMin/Max are the pixel sizes a bullet pin may
	// request. Below the minimum the copy is unreadable at 1080p; above the
	// maximum it cannot fit the presentation box. The bounds live beside the
	// payload contract so the run-level overlay_style.bullet block and a
	// per-scene pin validate against one source.
	EditorialBulletFontSizeMin = scriptpkg.BulletFontSizeMinPX
	EditorialBulletFontSizeMax = scriptpkg.BulletFontSizeMaxPX
	// editorialBulletMotionGroups are the registry groups an explicit bullet
	// motion must belong to. `bullet` is the authored group for the bullet
	// item kind; `phrase.all` keeps the shared phrase catalog selectable.
	editorialBulletMotionGroup = "bullet"
	editorialPhraseMotionGroup = "phrase.all"
)

// ValidateEditorialBulletStyles checks one payload's bullet pins. It validates
// shape and catalogue membership only; whether the named scene exists is the
// compiler's business (it owns the scene list).
func ValidateEditorialBulletStyles(styles []scriptpkg.EditorialBulletStyle) error {
	if len(styles) > MaxEditorialBulletStyles {
		return fmt.Errorf("editorial_bullet_styles cannot exceed %d entries", MaxEditorialBulletStyles)
	}
	seen := make(map[string]struct{}, len(styles))
	for i, style := range styles {
		ref := fmt.Sprintf("editorial_bullet_styles[%d]", i)
		sceneID := strings.TrimSpace(style.SceneID)
		if sceneID == "" {
			return fmt.Errorf("%s.scene_id is required", ref)
		}
		if _, duplicate := seen[sceneID]; duplicate {
			return fmt.Errorf("%s.scene_id %q is duplicated (a scene keeps at most one bullet)", ref, sceneID)
		}
		seen[sceneID] = struct{}{}
		if !scriptpkg.IsSupportedFontFamily(style.FontFamily) {
			return fmt.Errorf("%s.font_family %q is unsupported (%s)", ref, style.FontFamily, strings.Join(scriptpkg.SupportedFontFamilies, ", "))
		}
		if style.FontSizePX != 0 {
			if math.IsNaN(style.FontSizePX) || math.IsInf(style.FontSizePX, 0) ||
				style.FontSizePX < EditorialBulletFontSizeMin || style.FontSizePX > EditorialBulletFontSizeMax {
				return fmt.Errorf("%s.font_size_px must be in [%g,%g]", ref, EditorialBulletFontSizeMin, EditorialBulletFontSizeMax)
			}
		}
		if motion := strings.TrimSpace(style.MotionID); motion != "" {
			if !capanimreg.AnimationSelectable(motion, editorialBulletMotionGroup) &&
				!capanimreg.AnimationSelectable(motion, editorialPhraseMotionGroup) {
				return fmt.Errorf("%s.motion_id %q is unknown, retired, renderer-unsupported, or not a bullet motion", ref, motion)
			}
		}
	}
	return nil
}
