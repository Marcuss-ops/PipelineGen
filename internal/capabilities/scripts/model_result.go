// model_result.go — rendered-output contract: GenerateResult,
// final references and pipeline metrics (split from model.go).
package scriptgeneration

import (
	"math"
	"strconv"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// GenerationRun is the canonical aggregate that tracks a single
// execution of the script generation workflow. It is created BEFORE
// any external I/O (verdetto invariant: the POST handler creates
// the pipeline_run first).

// PhraseImpactResult is the kernel-owned extractive editorial summary contract.
// It is aliased here so capability callers keep the local name without a second
// divergent definition.
type PhraseImpactResult = scriptpkg.PhraseImpactResult

// PhraseImpactTimings is the worker's own stage breakdown, aliased for the
// same reason as PhraseImpactResult: callers keep the local name without a
// second divergent definition.
type PhraseImpactTimings = scriptpkg.PhraseImpactTimings

// ChartValueCandidate is a numeric label grounded as an exact source span by
// the VisualNER worker. The scripts layer re-checks Text with the strict chart
// parser before any value can authorize a chart.
type ChartValueCandidate struct {
	Text  string
	Value float64
}

// BulletCandidate is one VisualNER sentence bullet grounded as an exact
// source span. SentenceIndex is the worker's scene-local sentence number;
// the scripts layer re-maps it onto the Go sentence splitter before it can
// become an editorial bullet.
type BulletCandidate struct {
	ID, Kind, Evidence, Provenance string
	Text                           string
	SentenceIndex                  int
	Start                          int
	End                            int
	// Score is the semantic engine's grounded salience; it travels to
	// Phrase Impact so candidate ranking uses one shared priority function.
	Score float64
}

// SetSceneSemanticAnalyzer wires the unified semantic source. A batch-capable
// implementation analyses the whole script in one call; the per-category
// bullet/chart ports were retired with the cutover.
func (r *Runner) SetSceneSemanticAnalyzer(analyzer SceneSemanticAnalyzer) {
	if r != nil {
		r.sceneSemanticAnalyzer = analyzer
	}
}

// ── Visual chart values (merged from editorial_chart_values.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: chart candidates refine the rendered-output contract owned by
// this file. No behaviour change.

// maxEditorialChartValues mirrors the editorial manifest validation limit.
const maxEditorialChartValues = 6

// verifyChartCandidate turns one VisualNER chart row into the verbatim
// source surface it authorizes. The worker frequently mirrors the dirty NLP
// entity surface ("dove si registra solamente l'18") instead of the number
// alone, so the strict parser is applied to the cleaned numeric core and a
// %/currency suffix is recovered from the scene text ("l'18%" → "18%").
// The returned surface must still occur verbatim in the scene text and carry
// the worker's own value; anything else is rejected, never coerced.
func verifyChartCandidate(candidate ChartValueCandidate, sceneText string) (string, float64, bool) {
	if candidate.Text == "" {
		return "", 0, false
	}
	if parsed, ok := capabilityoverlay.ParseChartValue(candidate.Text); ok &&
		strings.Contains(sceneText, candidate.Text) && sameChartValue(parsed, candidate.Value) {
		return candidate.Text, parsed, true
	}
	cleaned := cleanNumericOverlayValue(candidate.Text)
	if cleaned == "" {
		return "", 0, false
	}
	surface := cleaned
	if span, ok := findEntitySpan(sceneText, cleaned); ok {
		surface = recoverNumericSuffix(sceneText, cleaned, []scriptpkg.AnnotationSpan{span})
	}
	if !strings.Contains(sceneText, surface) {
		return "", 0, false
	}
	parsed, ok := capabilityoverlay.ParseChartValue(surface)
	if !ok || !sameChartValue(parsed, candidate.Value) {
		return "", 0, false
	}
	return surface, parsed, true
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
		surface, parsed, ok := verifyChartCandidate(candidate, sceneText)
		if !ok {
			continue
		}
		id := chartValueIdentity(parsed)
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, surface)
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
		// NLP often emits the same spoken number twice ("42" NUMBER and
		// "42%" PERCENT on one word span). Keep one bar per (value, start):
		// duplicates would render twin bars for a single spoken fact.
		seen := make(map[string]int, len(input.Numbers))
		for _, number := range input.Numbers {
			parsed, ok := capabilityoverlay.ParseChartValue(strings.TrimSpace(number.Text))
			if !ok {
				continue
			}
			if _, ok := allowed[chartValueIdentity(parsed)]; !ok {
				continue
			}
			key := chartValueIdentity(parsed) + "\x00" + strconv.FormatInt(number.StartUS, 10)
			if idx, duplicate := seen[key]; duplicate {
				// Keep the unit-carrying surface ("42%" over "42") so
				// the rendered label never loses the spoken unit.
				if len(number.Text) > len(input.ChartNumbers[idx].Text) {
					input.ChartNumbers[idx] = number
				}
				continue
			}
			seen[key] = len(input.ChartNumbers)
			input.ChartNumbers = append(input.ChartNumbers, number)
		}
		return
	}
	// No Rust scene record means no chart authorization for this scene.
	input.ChartNumbers = []capabilityoverlay.TimedAnnotation{}
}

func chartValueIdentity(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// ── Render references (merged from model_render.go) ──
//
// Merged here to keep model_render.go under max_lines_per_file_strict=600:
// render lineage is part of the rendered-output contract. No behaviour change.

// overlayArtifactForItem resolves the certified clip for a semantic item from
// either the legacy per-item projection or a fused batch reference.
func overlayArtifactForItem(ref *contract.RenderReference, itemID string) (*contract.RenderArtifact, string, bool) {
	if ref == nil {
		return nil, "", false
	}
	for _, item := range ref.Items {
		if item.ItemID == itemID && item.Artifact != nil {
			return item.Artifact, item.JobID, true
		}
	}
	for _, batch := range ref.Batches {
		for _, id := range batch.ItemIDs {
			if id == itemID && batch.Artifact != nil {
				return batch.Artifact, batch.JobID, true
			}
		}
	}
	return nil, "", false
}
