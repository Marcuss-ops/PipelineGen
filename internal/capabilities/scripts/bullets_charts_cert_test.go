package scriptgeneration

// Certificazione plan-only Bullet Points + Chart Anims Modern (7 test).
// Mappa 1:1 la tabella del kit E2E citata nel task: C1/C2/C3/C4/B1/B2/M1.
// Livello plan-only (estrazione → piano → filtro → compilazione): il check
// video MP4 resta non eseguibile senza worker GPU e va certificato a parte.
//
//   C1 chart-positivo: 42% e 18% → 1 grafico, 2 barre, valori leggibili,
//     rapporto larghezze ~2.33, label sopravvive a `phrases: disabled`.
//   B1 bullet-positivo: 3 scene distinte → almeno 2 bullet renderizzati.
//   M1 combinato: bullet e grafici nello stesso video.
//   C2 chart disabilitato: `charts: disabled` → 0 grafici (label+barre).
//   B2 bullet disabilitati: `bullets: disabled` → 0 bullet.
//   C3 senza dati: testo senza numeri → nessun grafico inventato.
//   C4 scene separate: 42% in una scena, 18% in un'altra → nessun confronto.

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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

func certBullet(text string, startUS int64) capabilityoverlay.TimedAnnotation {
	return capabilityoverlay.TimedAnnotation{
		Text: text, PresentationRole: "scene_bullet",
		StartMs: startUS / 1000, EndMs: (startUS + 4_000_000 + 999) / 1000,
		StartUS: startUS, DurationUS: 4_000_000, Score: 0.99,
	}
}

func certScenes() []Scene {
	return []Scene{{ID: "s1", Index: 0}, {ID: "s2", Index: 1}, {ID: "s3", Index: 2}}
}

// C1 — Chart positivo: 42% e 18% nella stessa scena.
func TestCertC1ChartPositive(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{{ID: "s1", Numbers: certNumbers("42%", "18%"), ChartNumbers: certNumbers("42%", "18%")}}
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	require.Len(t, placements, 1, "C1: una scena con 2 valori → 1 placement")
	require.Equal(t, []string{"42%", "18%"}, placements[0].Labels)
	require.GreaterOrEqual(t, placements[0].WindowEndUS-placements[0].WindowStartUS, int64(2_000_000))

	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "c1", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	require.Equal(t, 1, plan.ChartCards)

	var label *capabilityoverlay.OverlayItem
	bars := []capabilityoverlay.OverlayItem{}
	for i := range plan.Items {
		if plan.Items[i].Params["chart_card"] == true {
			label = &plan.Items[i]
		}
		if plan.Items[i].Params["chart_bar"] == true {
			bars = append(bars, plan.Items[i])
		}
	}
	require.NotNil(t, label, "C1: label card presente")
	require.Contains(t, label.Text, "42%")
	require.Contains(t, label.Text, "18%")
	require.Len(t, bars, 2, "C1: 2 barre")
	w0, _ := bars[0].Params["width"].(float64)
	w1, _ := bars[1].Params["width"].(float64)
	require.Greater(t, w0, 0.0)
	require.Greater(t, w1, 0.0)
	ratio := w0 / w1
	assert.InDelta(t, 42.0/18.0, ratio, 0.05, "C1: rapporto larghezze ~2.33")

	// Regressione filtro categorie: `phrases: disabled` non deve mangiare
	// la label del grafico (kind text_phrase condiviso con le frasi).
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryCharts:  scriptpkg.ToggleEnabled,
	}}
	cat, ok := overlayItemCategory(*label)
	require.True(t, ok)
	require.Equal(t, scriptpkg.OverlayCategoryCharts, cat, "C1: label chart → categoria charts, non phrases")
	kept := filterOverlayItemsByCategory(plan.Items, []Scene{{ID: "s1"}}, canvas)
	assert.Equal(t, 3, countChartItems(kept), "C1: label+2 barre sopravvivono con phrases disabled")
}

// B1 — Bullet positivo: 3 scene distinte → almeno 2 bullet.
func TestCertB1BulletPositive(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{
		{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{certBullet("Il debito pubblico sale al nord", 0)}},
		{ID: "s2", Phrases: []capabilityoverlay.TimedAnnotation{certBullet("La produzione industriale rallenta in autunno", 10_000_000)}},
		{ID: "s3", Phrases: []capabilityoverlay.TimedAnnotation{certBullet("I consumi delle famiglie restano deboli", 20_000_000)}},
	}
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "b1", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Scenes: scenes,
	}, capabilityoverlay.AllCandidatesPlannerConfig(scenes))
	require.NoError(t, err)
	bullets := 0
	for _, item := range plan.Items {
		if role, _ := item.Params["editorial_role"].(string); role == "scene_bullet" {
			bullets++
			require.NotEmpty(t, strings.TrimSpace(item.Text))
		}
	}
	require.GreaterOrEqual(t, bullets, 2, "B1: almeno 2 bullet estratti e renderizzati")
	kept := filterOverlayItemsByCategory(plan.Items, certScenes(), OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleEnabled,
	}})
	keptBullets := 0
	for _, item := range kept {
		if role, _ := item.Params["editorial_role"].(string); role == "scene_bullet" {
			keptBullets++
		}
	}
	require.GreaterOrEqual(t, keptBullets, 2)
}

// M1 — Combinato: bullet e grafici nello stesso video.
func TestCertM1Combined(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{
		{ID: "s1", Numbers: certNumbers("42%", "18%"), ChartNumbers: certNumbers("42%", "18%"),
			Phrases: []capabilityoverlay.TimedAnnotation{certBullet("Il consenso cresce nelle grandi citta", 0)}},
		{ID: "s2", Phrases: []capabilityoverlay.TimedAnnotation{certBullet("La fiducia dei consumatori scende ancora", 20_000_000)}},
	}
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	require.Len(t, placements, 1)
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "m1", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.AllCandidatesPlannerConfig(scenes))
	require.NoError(t, err)
	hasChart, hasBullet := false, false
	for _, item := range plan.Items {
		if item.Params["chart_card"] == true || item.Params["chart_bar"] == true {
			hasChart = true
		}
		if role, _ := item.Params["editorial_role"].(string); role == "scene_bullet" {
			hasBullet = true
		}
	}
	require.True(t, hasChart, "M1: grafici presenti")
	require.True(t, hasBullet, "M1: bullet presenti")
}

// C2 — Chart disabilitato: `charts: disabled` → 0 grafici (label E barre).
func TestCertC2ChartsDisabled(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{{ID: "s1", Numbers: certNumbers("42%", "18%"), ChartNumbers: certNumbers("42%", "18%")}}
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	require.Len(t, placements, 1)
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "c2", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	require.Equal(t, 3, countChartItems(plan.Items))
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryCharts: scriptpkg.ToggleDisabled,
	}}
	kept := filterOverlayItemsByCategory(plan.Items, []Scene{{ID: "s1"}}, canvas)
	assert.Equal(t, 0, countChartItems(kept), "C2: charts disabled → 0 grafici, barre incluse")

	// Prefiltro: senza charts attivi i ChartNumbers vengono azzerati.
	pref := dropCategoryDisabledCandidates(
		&capabilityoverlay.SceneInput{ID: "s1", Numbers: certNumbers("42%", "18%"), ChartNumbers: certNumbers("42%", "18%")},
		"s1", 0, OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryCharts: scriptpkg.ToggleDisabled,
		}})
	if pref != nil {
		assert.Empty(t, pref.ChartNumbers)
	}
}

// B2 — Bullet disabilitati: `bullets: disabled` → 0 bullet.
func TestCertB2BulletsDisabled(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{
		{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{certBullet("Il debito pubblico sale al nord", 0)}},
	}
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "b2", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Scenes: scenes,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	require.NotEmpty(t, plan.Items)
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleDisabled,
	}}
	kept := filterOverlayItemsByCategory(plan.Items, []Scene{{ID: "s1"}}, canvas)
	for _, item := range kept {
		role, _ := item.Params["editorial_role"].(string)
		assert.NotEqual(t, "scene_bullet", role, "B2: bullets disabled → 0 bullet")
		assert.NotEqual(t, "chapter_bullet", role)
	}

	// A composed chain names only its coalescing family at the container
	// level; the bullet role lives on the segment params. The category
	// filter must inspect the children, otherwise `bullets: disabled`
	// renders the fused bullet anyway.
	chain := capabilityoverlay.OverlayItem{
		ID: "b2-chain", SceneID: "s1", Kind: capabilityoverlay.KindPhraseChain, TemplateID: "PHRASE_CHAIN",
		StartMs: 0, EndMs: 6000, StartUS: 0, DurationUS: 6_000_000,
		Params: map[string]any{"chain_segments": []capabilityoverlay.ChainSegment{
			{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Il debito pubblico sale al nord", StartUS: 0, DurationUS: 4_000_000,
				Params: map[string]any{"editorial_role": "scene_bullet"}},
			{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "nuove misure per contenere la spesa", StartUS: 4_000_000, DurationUS: 2_000_000},
		}},
	}
	kept = filterOverlayItemsByCategory([]capabilityoverlay.OverlayItem{chain}, []Scene{{ID: "s1"}}, canvas)
	assert.Empty(t, kept, "B2: una catena con un segmento bullet è un gesto atomico e cade con bullets disabled")
}

// C3 — Senza dati: testo senza numeri → nessun grafico inventato.
func TestCertC3NoData(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{{ID: "s1", Phrases: []capabilityoverlay.TimedAnnotation{{
		Text: "Il paese cresce senza numeri precisi nel testo", StartUS: 0, DurationUS: 8_000_000, Score: 0.9,
	}}}}
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	assert.Empty(t, placements, "C3: nessun numero → nessun placement")
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "c3", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	assert.Equal(t, 0, plan.ChartCards)
	assert.Equal(t, 0, countChartItems(plan.Items))
	for _, tok := range []string{"abc", "circa", "molto", "tanto"} {
		_, ok := capabilityoverlay.ParseChartValue(tok)
		assert.False(t, ok, "C3: %q non deve parsare come valore", tok)
	}
}

// C4 — Scene separate: 42% in una scena, 18% in un'altra → nessun confronto.
func TestCertC4SeparateScenes(t *testing.T) {
	scenes := []capabilityoverlay.SceneInput{
		{ID: "s1", Numbers: certNumbers("42%"), ChartNumbers: certNumbers("42%")},
		{ID: "s2", Numbers: certNumbers("18%"), ChartNumbers: certNumbers("18%")},
	}
	// Allinea le finestre alle scene reali: certNumbers parte da 0 in ogni
	// scena, ma le due scene occupano finestre diverse sulla timeline.
	scenes[1].Numbers[0].StartUS = 20_000_000
	scenes[1].Numbers[0].StartMs = 20_000
	scenes[1].Numbers[0].EndMs = 28_000
	scenes[1].ChartNumbers[0].StartUS = 20_000_000
	scenes[1].ChartNumbers[0].StartMs = 20_000
	scenes[1].ChartNumbers[0].EndMs = 28_000
	placements := capabilityoverlay.ChartPlacementsForScenes(scenes)
	assert.Empty(t, placements, "C4: un valore per scena → nessun confronto artificiale")
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "c4", VideoID: "v1", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: scenes, Charts: placements,
	}, capabilityoverlay.PlannerConfig{})
	require.NoError(t, err)
	assert.Equal(t, 0, plan.ChartCards)
	assert.Equal(t, 0, countChartItems(plan.Items))
	_ = math.Abs
}
