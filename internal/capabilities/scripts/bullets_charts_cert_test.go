package scriptgeneration

// Certificazione plan-only Bullet Points + Chart Anim Modern (Go survivor).
//
// Dopo la demolizione del planner Go (2026-11-18) la derivazione dei placement
// chart è Rust-owned: la copre BrainRust/semantic-engine/tests/
// chart_acceptance_36.rs, insieme a overlay-compiler/tests/
// overlay_plan_contract.rs::charts_and_chapter_recaps_reach_the_final_plan.
// Qui resta solo la superficie Go sopravvissuta: filtro categorie, prefiltro
// `charts: disabled`, catena bullet atomica e parsing stretto dei valori.
//
//   C1 chart-positivo: label+barre sopravvivono con `phrases: disabled`.
//   C2 chart disabilitato: `charts: disabled` → ChartNumbers azzerati.
//   B2 bullet disabilitati: catena con segmento bullet → 0 item.
//   C3 senza dati: un testo non numerico non deve parsare come valore.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func certNumbers(texts ...string) []capabilityoverlay.TimedAnnotation {
	out := make([]capabilityoverlay.TimedAnnotation, 0, len(texts))
	var cursor int64
	for _, text := range texts {
		out = append(out, capabilityoverlay.TimedAnnotation{
			Text: text, Type: "PERCENT",
			StartMs: cursor / 1000, EndMs: (cursor + 8_000_000 + 999) / 1000,
			StartUS: cursor, DurationUS: 8_000_000, Score: 0.9,
		})
		cursor += 10_000_000
	}
	return out
}

// C1 — Chart positivo: label e barre sono un'unità atomica della categoria
// charts, non phrases.
func TestCertC1ChartPositive(t *testing.T) {
	label := capmodel.OverlayItem{
		ID: "chart-label", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
		Text: "42%\n18%", Params: map[string]any{"chart_card": true},
	}
	bars := []capmodel.OverlayItem{
		{ID: "bar-0", SceneID: "s1", Kind: "shape", TemplateID: "SHAPE", Params: map[string]any{"chart_bar": true}},
		{ID: "bar-1", SceneID: "s1", Kind: "shape", TemplateID: "SHAPE", Params: map[string]any{"chart_bar": true}},
	}
	cat, ok := overlayItemCategory(label)
	require.True(t, ok)
	require.Equal(t, scriptpkg.OverlayCategoryCharts, cat, "C1: label chart → categoria charts, non phrases")
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryCharts:  scriptpkg.ToggleEnabled,
	}}
	kept := filterOverlayItemsByCategory(append([]capmodel.OverlayItem{label}, bars...), []Scene{{ID: "s1"}}, canvas)
	assert.Equal(t, 3, countChartItems(kept), "C1: label+2 barre sopravvivono con phrases disabled")
}

// C2 — Chart disabilitato: `charts: disabled` → 0 grafici (label E barre).
func TestCertC2ChartsDisabled(t *testing.T) {
	pref := dropCategoryDisabledCandidates(
		&capabilityoverlay.SceneInput{ID: "s1", Numbers: certNumbers("42%", "18%"), ChartNumbers: certNumbers("42%", "18%")},
		"s1", 0, OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryCharts: scriptpkg.ToggleDisabled,
		}})
	require.NotNil(t, pref)
	assert.Empty(t, pref.ChartNumbers)
}

// B2 — Bullet disabilitati: `bullets: disabled` → 0 bullet.
func TestCertB2BulletsDisabled(t *testing.T) {
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleDisabled,
	}}
	// A composed chain names only its coalescing family at the container
	// level; the bullet role lives on the segment params. The category
	// filter must inspect the children, otherwise `bullets: disabled`
	// renders the fused bullet anyway.
	chain := capmodel.OverlayItem{
		ID: "b2-chain", SceneID: "s1", Kind: capmodel.KindPhraseChain, TemplateID: "PHRASE_CHAIN",
		StartMs: 0, EndMs: 6000, StartUS: 0, DurationUS: 6_000_000,
		Params: map[string]any{"chain_segments": []capmodel.ChainSegment{
			{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Il debito pubblico sale al nord", StartUS: 0, DurationUS: 4_000_000,
				Params: map[string]any{"editorial_role": "scene_bullet"}},
			{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "nuove misure per contenere la spesa", StartUS: 4_000_000, DurationUS: 2_000_000},
		}},
	}
	kept := filterOverlayItemsByCategory([]capmodel.OverlayItem{chain}, []Scene{{ID: "s1"}}, canvas)
	assert.Empty(t, kept, "B2: una catena con un segmento bullet è un gesto atomico e cade con bullets disabled")
}

// C3 — Senza dati: testo senza numeri → nessun valore inventato.
func TestCertC3NoData(t *testing.T) {
	for _, tok := range []string{"abc", "circa", "molto", "tanto"} {
		_, ok := capabilityoverlay.ParseChartValue(tok)
		assert.False(t, ok, "C3: %q non deve parsare come valore", tok)
	}
}
