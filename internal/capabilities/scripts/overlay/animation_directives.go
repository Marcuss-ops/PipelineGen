// Package overlay — animation_directives.go: canonical animation contract (§4).
//
// I vecchi campi (phrase_motions, image_motions, entity_image_motions,
// entity_caption_motions, animation_policies, map_camera_animation,
// map_label_animation, map_animation) restano accettati come ingressi
// compatibili, ma il builder li normalizza UNA sola volta in
// AnimationDirective. Precedenza: exact elemento > exact scena > prefer
// richiesta > profilo canale > auto. Una scelta exact incompatibile non è mai
// sostituita silenziosamente: fail-closed con errore.
package overlay

import (
	"fmt"
	"strings"
)

// DirectiveMode è auto/prefer/exact (§4).
type DirectiveMode string

const (
	DirectiveAuto   DirectiveMode = "auto"
	DirectivePrefer DirectiveMode = "prefer"
	DirectiveExact  DirectiveMode = "exact"
)

// DirectiveTarget distingue motion/composition/camera/label/map-treatment.
// Non unificare cose con significati differenti (§3 catalogo).
type DirectiveTarget string

const (
	TargetImportantPhrase DirectiveTarget = "important_phrase"
	TargetEntityImage     DirectiveTarget = "entity_image"
	TargetCaption         DirectiveTarget = "caption"
	TargetImage           DirectiveTarget = "image"
	TargetMapCamera       DirectiveTarget = "map_camera"
	TargetMapComposition  DirectiveTarget = "map_composition"
	TargetMapLabel        DirectiveTarget = "map_label"
	TargetDate            DirectiveTarget = "date"
	TargetMetric          DirectiveTarget = "metric"
)

// AnimationDirective è la rappresentazione canonica: il modello editoriale
// dichiara scena/intervallo semantico, tipo contenuto, entità opzionale,
// preferenza e parametri — mai il futuro item.ID dell'overlay non ancora costruito.
type AnimationDirective struct {
	Scope    string // "scene" (unico supportato oggi)
	SceneRef string
	Target   DirectiveTarget
	Mode     DirectiveMode
	MotionID string
	Family   string
	Params   map[string]any
}

// Validate fail-closed: exact richiede MotionID, scope/scene/target obbligatori.
func (d AnimationDirective) Validate() error {
	if strings.TrimSpace(d.Scope) == "" {
		return fmt.Errorf("animation directive: scope is required")
	}
	if strings.TrimSpace(d.SceneRef) == "" {
		return fmt.Errorf("animation directive: scene_ref is required")
	}
	if strings.TrimSpace(string(d.Target)) == "" {
		return fmt.Errorf("animation directive: target is required")
	}
	switch d.Mode {
	case DirectiveAuto, DirectivePrefer, DirectiveExact, "":
	default:
		return fmt.Errorf("animation directive: unsupported mode %q", d.Mode)
	}
	if d.Mode == DirectiveExact && strings.TrimSpace(d.MotionID) == "" {
		return fmt.Errorf("animation directive: exact mode requires motion_id (target %q scene %q)", d.Target, d.SceneRef)
	}
	return nil
}

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
