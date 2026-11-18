package scriptgeneration

// Live-wiring certification for the surviving Go chart surface: Rust authorizes
// the values, Go refuses anything Rust did not certify. Plan admission and the
// emitted card/bars are covered by
// BrainRust/semantic-engine/tests/chart_acceptance_36.rs and
// BrainRust/overlay-compiler/tests/overlay_plan_contract.rs::
// charts_and_chapter_recaps_reach_the_final_plan.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func chartWiringNumbers() []capabilityoverlay.TimedAnnotation {
	mk := func(text string, startUS int64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{
			Text: text, Type: "PERCENT",
			StartMs: startUS / 1000, EndMs: (startUS + 8_000_000 + 999) / 1000,
			StartUS: startUS, DurationUS: 8_000_000, Score: 0.9,
		}
	}
	return []capabilityoverlay.TimedAnnotation{mk("42%", 0), mk("18%", 10_000_000)}
}

func TestRustChartValuesRestrictToGroundedNumbers(t *testing.T) {
	numbers := append(chartWiringNumbers(), capabilityoverlay.TimedAnnotation{
		Text: "9%", Type: "PERCENT", StartMs: 20_000, EndMs: 28_000,
		StartUS: 20_000_000, DurationUS: 8_000_000, Score: 0.9,
	})
	input := &capabilityoverlay.SceneInput{ID: "s1", Numbers: numbers}
	applyRustChartValues(input, []scriptpkg.SceneHighlight{{
		SceneID: "s1", ChartValues: []string{"42%", "18%"},
	}}, "s1", true)
	require.Len(t, input.ChartNumbers, 2)
	assert.Equal(t, "42%", input.ChartNumbers[0].Text)
	assert.Equal(t, "18%", input.ChartNumbers[1].Text, "unapproved grounded third value is excluded")

	applyRustChartValues(input, nil, "s1", true)
	assert.Empty(t, input.ChartNumbers, "missing Rust extraction must explicitly disable charts")
	input.ChartNumbers = nil
	applyRustChartValues(input, []scriptpkg.SceneHighlight{{SceneID: "s1", ChartValues: []string{"42%", "18%"}}}, "s1", false)
	assert.Empty(t, input.ChartNumbers, "uncertified Rust extraction must never authorize charts")
}
