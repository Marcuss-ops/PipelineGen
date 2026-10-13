package scriptgeneration

import (
	"context"
	"math"
	"strconv"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// visualChartCandidateLimit bounds the VisualNER entities requested per scene
// so a numeric label is not truncated by the entity limit.
const visualChartCandidateLimit = 12

// maxEditorialChartValues mirrors the editorial manifest validation limit.
const maxEditorialChartValues = 6

// attachVisualChartValues adds VisualNER chart candidates to each scene's
// ChartValues. A candidate survives only if its text is an exact substring of
// the scene text and the strict chart parser reads it as the worker's value.
// Failures keep the existing values; they never fail the generation.
func (r *Runner) attachVisualChartValues(ctx context.Context, language string, inputs []scriptpkg.SceneAnalysisInput, highlights []scriptpkg.SceneHighlight) []scriptpkg.SceneHighlight {
	if r == nil || r.sceneChartAnalyzer == nil || len(highlights) == 0 {
		return highlights
	}
	texts := make(map[string]string, len(inputs))
	for _, in := range inputs {
		texts[in.SceneID] = in.Text
	}
	for i := range highlights {
		text := texts[highlights[i].SceneID]
		if strings.TrimSpace(text) == "" {
			continue
		}
		candidates, err := r.sceneChartAnalyzer.ChartCandidates(ctx, language, text, visualChartCandidateLimit)
		if err != nil {
			r.log.Warn("visualner chart analysis unavailable; keeping existing chart values",
				zap.String("scene_id", highlights[i].SceneID), zap.Error(err))
			continue
		}
		highlights[i].ChartValues = mergeChartValues(highlights[i].ChartValues, candidates, text)
	}
	return highlights
}

// mergeChartValues appends verified candidates to existing values, keeping
// the manifest's no-duplicate and maximum-count rules.
func mergeChartValues(existing []string, candidates []ChartValueCandidate, sceneText string) []string {
	out := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		if parsed, ok := capabilityoverlay.ParseChartValue(value); ok {
			seen[chartValueIdentity(parsed)] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if len(out) >= maxEditorialChartValues {
			break
		}
		if candidate.Text == "" || !strings.Contains(sceneText, candidate.Text) {
			continue
		}
		parsed, ok := capabilityoverlay.ParseChartValue(candidate.Text)
		if !ok || !sameChartValue(parsed, candidate.Value) {
			continue
		}
		id := chartValueIdentity(parsed)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, candidate.Text)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sameChartValue(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(a))
}

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
