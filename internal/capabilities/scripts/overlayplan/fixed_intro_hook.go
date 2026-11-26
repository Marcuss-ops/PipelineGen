package overlayplan

import (
	"fmt"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const fixedIntroHookWindowUS int64 = 3_000_000

// appendFixedIntroHook puts the caller's short display line over the first
// three seconds of its protected intro clip. Fixed-media scenes are deliberately
// excluded from Rust's speech-timed overlay request, so this item is authored
// at the fixed scene's absolute timeline origin before the plan is sealed.
func appendFixedIntroHook(plan *capmodel.OverlayPlan, result *contract.GenerateResult, language contract.Language) {
	if plan == nil || result == nil {
		return
	}
	for _, scene := range result.Scenes {
		if scene.Role != scriptpkg.SceneRoleOpening || !scene.ExecutionMode.IsFixedMedia() {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			continue
		}
		duration := fixedIntroHookWindowUS
		if scene.DurationUS > 0 && scene.DurationUS < duration {
			duration = scene.DurationUS
		}
		if duration <= 0 {
			continue
		}
		id := fmt.Sprintf("fixed-intro-hook-%s", digest.SHA256String(scene.ID + "\x00" + text)[:16])
		plan.Items = append(plan.Items, capmodel.OverlayItem{
			ID:         id,
			SceneID:    scene.ID,
			Kind:       "text_phrase",
			StartMs:    0,
			EndMs:      (duration + 999) / 1000,
			StartUS:    0,
			DurationUS: duration,
			TemplateID: "IMPORTANT_PHRASE",
			PresetID:   "phrase_default",
			MotionID:   "typewriter_word_step_short",
			MotionParams: map[string]any{
				"enter_frames": 8,
			},
			Text: text,
			Style: map[string]any{
				"fill":        "#000000",
				"font_family": "oswald",
				"font_size":   96.0,
				"glow":        map[string]any{"color": "#FFFFFF"},
			},
			Params: map[string]any{
				"editorial_candidate": "hook",
				"editorial_role":      "hook",
				"fill":                "#000000",
				"font_family":         "oswald",
				"font_size_px":        96.0,
				"glow_color":          "#FFFFFF",
				"glow_size":           10,
				"height":              600,
				"phrase_family":       "short_phrase",
				"position":            "center",
				"priority":            1.2,
				"width":               1700,
			},
		})
		return
	}
}
