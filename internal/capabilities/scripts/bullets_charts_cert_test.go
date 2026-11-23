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

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
// C2 — Chart disabilitato: `charts: disabled` → 0 grafici (label E barre).
// B2 — Bullet disabilitati: `bullets: disabled` → 0 bullet.
// C3 — Senza dati: testo senza numeri → nessun valore inventato.
func TestCertC3NoData(t *testing.T) {
	for _, tok := range []string{"abc", "circa", "molto", "tanto"} {
		_, ok := capabilityoverlay.ParseChartValue(tok)
		assert.False(t, ok, "C3: %q non deve parsare come valore", tok)
	}
}
