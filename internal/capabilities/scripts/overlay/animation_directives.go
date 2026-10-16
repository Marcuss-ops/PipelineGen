// Package overlay — animation_directives.go: single normalization entry point
// for the canonical animation contract (§4, P2-2).
//
// Il contratto canonico vive UNA sola volta in kernel/script
// (AnimationDirective/Mode/Target): qui solo alias + normalizzazione, mai un
// secondo sistema di tipi parallelo. I vecchi campi (phrase_motions,
// image_motions, entity_image_motions, entity_caption_motions,
// animation_policies, map_camera_animation, map_label_animation,
// map_animation) restano ingressi compatibili normalizzati UNA sola volta.
// Precedenza: exact elemento > exact scena > prefer richiesta > profilo
// canale > auto. Una scelta exact incompatibile non è mai sostituita
// silenziosamente: fail-closed con errore.
package overlay

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Canonical contract aliases: exactly one type system (kernel/script).
type (
	AnimationDirective       = scriptpkg.AnimationDirective
	AnimationDirectiveMode   = scriptpkg.AnimationDirectiveMode
	AnimationDirectiveTarget = scriptpkg.AnimationDirectiveTarget
)

const (
	DirectiveAuto   = scriptpkg.AnimationDirectiveAuto
	DirectivePrefer = scriptpkg.AnimationDirectivePrefer
	DirectiveExact  = scriptpkg.AnimationDirectiveExact

	TargetImportantPhrase = scriptpkg.AnimationTargetImportantPhrase
	TargetEntityImage     = scriptpkg.AnimationTargetEntityImage
	TargetCaption         = scriptpkg.AnimationTargetCaption
	TargetImage           = scriptpkg.AnimationTargetImage
	TargetMapCamera       = scriptpkg.AnimationTargetMapCamera
	TargetMapComposition  = scriptpkg.AnimationTargetMapComposition
	TargetMapLabel        = scriptpkg.AnimationTargetMapLabel
	TargetDate            = scriptpkg.AnimationTargetDate
	TargetMetric          = scriptpkg.AnimationTargetMetric
)

// NormalizeLegacyFields è il singolo punto di normalizzazione: i vecchi campi
// run-level vengono proiettati in direttive con precedenza documentata.
// Oggi proietta solo le scelte exact per scena (mappa/camera/label) e lascia
// le pool automatiche al selector; l'estensione per-lane è incrementale.
func NormalizeLegacyFields(sceneRef string, mapCamera, mapLabel, mapAnimation string) []AnimationDirective {
	var out []AnimationDirective
	if v := strings.TrimSpace(mapCamera); v != "" {
		out = append(out, AnimationDirective{Scope: "scene", SceneRef: sceneRef, Target: TargetMapCamera, Mode: DirectiveExact, MotionID: v})
	}
	if v := strings.TrimSpace(mapLabel); v != "" {
		out = append(out, AnimationDirective{Scope: "scene", SceneRef: sceneRef, Target: TargetMapLabel, Mode: DirectiveExact, MotionID: v})
	}
	if v := strings.TrimSpace(mapAnimation); v != "" {
		out = append(out, AnimationDirective{Scope: "scene", SceneRef: sceneRef, Target: TargetMapComposition, Mode: DirectiveExact, MotionID: v})
	}
	return out
}
