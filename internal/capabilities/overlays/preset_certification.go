// Package overlays — preset_certification.go owns the preset certification
// matrix: the 16 controlled mini-render cells (Name 3, Phrase 5, Word 3,
// Image 5) that certify each Chronon visual preset end-to-end. It is the
// single source of truth shared by the compile-level matrix test and the
// live mini-render harness.
package overlays

import kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

// PresetCertificationCell is one mini-render cell of the matrix: a preset
// bound to its semantic template and content.
type PresetCertificationCell struct {
	// Family is the editorial family: "name", "phrase", "word" or "image".
	Family string
	// Preset is the canonical Chronon VisualPresetRegistry id (owned by
	// Chronon3d, never invented here).
	Preset string
	// Template is the semantic template the preset is applied to.
	Template string
	// Text is the surface text for text-driven presets (empty for images).
	Text string
}

// PresetCertificationMatrix returns the frozen 16-cell matrix. fast_fade_through
// and phrase_word_reveal appear in both the Phrase and Word families by design,
// so the cell count is 16 (not the number of distinct preset ids).
func PresetCertificationMatrix() []PresetCertificationCell {
	const name = "Michael Jordan"
	const phrase = "MICHAEL JORDAN CHANGED BASKETBALL"
	const word = "LEGEND"
	return []PresetCertificationCell{
		// Name (3)
		{Family: "name", Preset: "name_glow_typewriter", Template: "PERSON", Text: name},
		{Family: "name", Preset: "name_glow_slide", Template: "PERSON", Text: name},
		{Family: "name", Preset: "name_glow_pop", Template: "PERSON", Text: name},
		// Phrase (5)
		{Family: "phrase", Preset: "fast_fade_through", Template: "IMPORTANT_PHRASE", Text: phrase},
		{Family: "phrase", Preset: "clean_slide_up", Template: "IMPORTANT_PHRASE", Text: phrase},
		{Family: "phrase", Preset: "slide_lateral", Template: "IMPORTANT_PHRASE", Text: phrase},
		{Family: "phrase", Preset: "phrase_word_reveal", Template: "IMPORTANT_PHRASE", Text: phrase},
		{Family: "phrase", Preset: "undertext_pop", Template: "IMPORTANT_PHRASE", Text: phrase},
		// Word (3)
		{Family: "word", Preset: "snap_scale", Template: "IMPORTANT_WORD", Text: word},
		{Family: "word", Preset: "fast_fade_through", Template: "IMPORTANT_WORD", Text: word},
		{Family: "word", Preset: "phrase_word_reveal", Template: "IMPORTANT_WORD", Text: word},
		// Image (5)
		{Family: "image", Preset: "image_fast_fade", Template: "IMAGE_OVERLAY"},
		{Family: "image", Preset: "image_slide_left", Template: "IMAGE_OVERLAY"},
		{Family: "image", Preset: "image_slide_right", Template: "IMAGE_OVERLAY"},
		{Family: "image", Preset: "modern_rounded_pop", Template: "IMAGE_OVERLAY"},
		{Family: "image", Preset: "bottom_card_rise", Template: "IMAGE_OVERLAY"},
	}
}

// BuildPresetCertificationPlan builds the 5s assembly-profile mini-render plan for
// one matrix cell: a background video below a single overlay item carrying the
// cell's explicit preset. The image fixture is the golden globe asset; text
// cells carry their surface text.
func BuildPresetCertificationPlan(cell PresetCertificationCell, planID string) OverlayPlan {
	assembly := kernelmedia.DefaultAssemblyMediaContractV2()
	item := OverlayItem{
		ID:         cell.Family + "-" + cell.Preset,
		TemplateID: cell.Template,
		PresetID:   cell.Preset,
		StartMs:    500,
		EndMs:      3500,
	}
	if cell.Template == "IMAGE_OVERLAY" {
		item.AssetRefs = []OverlayAssetRef{{
			AssetID: "fixture",
			URL:     "assets/overlay_globe.png",
			SHA256:  GoldenGlobeHash,
		}}
	} else {
		item.Text = cell.Text
	}
	return OverlayPlan{
		SchemaVersion: SchemaVersionPlan,
		PlanID:        planID,
		VideoID:       "video-" + planID,
		ProjectID:     "preset-certification",
		Width:         assembly.Width,
		Height:        assembly.Height,
		FPSNum:        assembly.FPS.Num, FPSDen: assembly.FPS.Den,
		RendererVersion: "chronon",
		Items: []OverlayItem{
			{
				ID:         "background_video",
				TemplateID: "VIDEO_BACKGROUND",
				StartMs:    0,
				EndMs:      5000,
				AssetRefs: []OverlayAssetRef{{
					AssetID: "background",
					URL:     "assets/background.mp4",
					SHA256:  GoldenBackgroundVideoHash,
				}},
			},
			item,
		},
	}
}
