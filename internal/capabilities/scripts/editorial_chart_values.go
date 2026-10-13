package scriptgeneration

import (
	"strconv"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// applyRustChartValues lets the Rust scene analysis authorize chart candidates
// without turning model output into data: only exact values already present in
// the scene's grounded number annotations survive, with their certified timing.
func applyRustChartValues(input *capabilityoverlay.SceneInput, highlights []scriptpkg.SceneHighlight, sceneID string, certified bool) {
	if input == nil {
		return
	}
	if !certified {
		input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
		return
	}
	for _, highlight := range highlights {
		if highlight.SceneID != sceneID {
			continue
		}
		if len(highlight.ChartValues) == 0 {
			input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
			return
		}
		allowed := make(map[string]struct{}, len(highlight.ChartValues))
		for _, value := range highlight.ChartValues {
			parsed, ok := capabilityoverlay.ParseChartValue(value)
			if !ok {
				continue
			}
			allowed[chartValueIdentity(parsed)] = struct{}{}
		}
		if len(allowed) < 2 {
			input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
			return
		}
		input.ChartNumbers = make([]capabilityoverlay.TimedAnnotation, 0, len(input.Numbers))
		for _, number := range input.Numbers {
			parsed, ok := capabilityoverlay.ParseChartValue(strings.TrimSpace(number.Text))
			if !ok {
				continue
			}
			if _, ok := allowed[chartValueIdentity(parsed)]; ok {
				input.ChartNumbers = append(input.ChartNumbers, number)
			}
		}
		return
	}
	// No Rust scene record means no chart authorization for this scene.
	input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
}

func chartValueIdentity(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
