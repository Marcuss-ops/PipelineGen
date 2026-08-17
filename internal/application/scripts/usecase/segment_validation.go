package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

const (
	defaultSegmentWordsTolerancePercent = 15.0
	defaultTotalWordsTolerancePercent   = 10.0
	defaultMaxSegmentRegeneration       = 2
)

type segmentValidationSettings struct {
	segmentTolerancePercent float64
	totalTolerancePercent   float64
	maxRegenerationAttempts int
}

func (e *Engine) segmentSettings() segmentValidationSettings {
	settings := segmentValidationSettings{
		segmentTolerancePercent: e.segmentWordsTolerancePercent,
		totalTolerancePercent:   e.totalWordsTolerancePercent,
		maxRegenerationAttempts: e.maxSegmentRegenerationAttempts,
	}
	if settings.segmentTolerancePercent <= 0 {
		settings.segmentTolerancePercent = defaultSegmentWordsTolerancePercent
	}
	if settings.totalTolerancePercent <= 0 {
		settings.totalTolerancePercent = defaultTotalWordsTolerancePercent
	}
	if settings.maxRegenerationAttempts <= 0 {
		settings.maxRegenerationAttempts = defaultMaxSegmentRegeneration
	}
	return settings
}

// ConfigureSegmentValidation sets the bounded segment QA policy. Zero values
// select the canonical defaults; negative retry counts are clamped to zero.
func (e *Engine) ConfigureSegmentValidation(segmentTolerancePercent, totalTolerancePercent float64, maxRegenerationAttempts int) {
	if e == nil {
		return
	}
	e.segmentWordsTolerancePercent = segmentTolerancePercent
	e.totalWordsTolerancePercent = totalTolerancePercent
	e.maxSegmentRegenerationAttempts = maxRegenerationAttempts
}

type segmentBudget struct {
	Target int
	Min    int
	Max    int
}

func segmentBudgetFor(plan *scriptpkg.ResolvedGenerationPlan, index int, tolerancePercent float64) segmentBudget {
	if tolerancePercent <= 0 {
		tolerancePercent = defaultSegmentWordsTolerancePercent
	}
	segment := plan.Segments[index]
	target := segment.TargetWords
	if target <= 0 {
		target = plan.SegmentWords
	}
	if target <= 0 {
		target = plan.TargetWords
	}
	if target <= 0 {
		target = 80
	}
	minWords := segment.MinWords
	if minWords <= 0 {
		minWords = int(math.Floor(float64(target) * (1 - tolerancePercent/100)))
	}
	maxWords := segment.MaxWords
	if maxWords <= 0 {
		maxWords = int(math.Ceil(float64(target) * (1 + tolerancePercent/100)))
	}
	return segmentBudget{Target: target, Min: minWords, Max: maxWords}
}

type segmentValidationReport struct {
	Valid          bool
	InvalidIndexes []int
	ActualTotal    int
	TotalTarget    int
	TotalMin       int
	TotalMax       int
	Reasons        []string
}

func validateSegmentTexts(plan *scriptpkg.ResolvedGenerationPlan, texts []string, settings segmentValidationSettings) segmentValidationReport {
	report := segmentValidationReport{Valid: true}
	if plan == nil || len(plan.Segments) == 0 {
		return report
	}
	if len(texts) != len(plan.Segments) {
		report.Valid = false
		report.InvalidIndexes = make([]int, len(plan.Segments))
		for i := range plan.Segments {
			report.InvalidIndexes[i] = i
		}
		report.Reasons = append(report.Reasons, fmt.Sprintf("expected %d segment paragraphs, got %d", len(plan.Segments), len(texts)))
		return report
	}

	invalid := make(map[int]struct{})
	for i, text := range texts {
		budget := segmentBudgetFor(plan, i, settings.segmentTolerancePercent)
		actual := textutil.CountWords(text)
		if actual < budget.Min || actual > budget.Max {
			invalid[i] = struct{}{}
			report.Reasons = append(report.Reasons,
				fmt.Sprintf("segment[%d] words=%d outside [%d,%d] target=%d", i, actual, budget.Min, budget.Max, budget.Target))
		}
	}

	totalTarget := plan.TargetWords
	if totalTarget <= 0 {
		for i := range plan.Segments {
			totalTarget += segmentBudgetFor(plan, i, settings.segmentTolerancePercent).Target
		}
	}
	totalMin := int(math.Floor(float64(totalTarget) * (1 - settings.totalTolerancePercent/100)))
	totalMax := int(math.Ceil(float64(totalTarget) * (1 + settings.totalTolerancePercent/100)))
	actualTotal := 0
	for _, text := range texts {
		actualTotal += textutil.CountWords(text)
	}
	report.ActualTotal = actualTotal
	report.TotalTarget = totalTarget
	report.TotalMin = totalMin
	report.TotalMax = totalMax
	if actualTotal < totalMin || actualTotal > totalMax {
		report.Valid = false
		report.Reasons = append(report.Reasons,
			fmt.Sprintf("total words=%d outside [%d,%d] target=%d", actualTotal, totalMin, totalMax, totalTarget))
		// A total-only failure has no single objectively invalid segment.
		// Keep already-valid text frozen and make only currently mutable
		// segments eligible for the next regeneration. If every segment
		// passed its own gate, choose the segment furthest from its target
		// as the smallest possible mutable surface.
		if len(invalid) == 0 {
			best := 0
			bestDistance := -1
			for i, text := range texts {
				budget := segmentBudgetFor(plan, i, settings.segmentTolerancePercent)
				distance := absInt(textutil.CountWords(text) - budget.Target)
				if distance > bestDistance {
					best, bestDistance = i, distance
				}
			}
			invalid[best] = struct{}{}
		}
	}
	for i := range invalid {
		report.InvalidIndexes = append(report.InvalidIndexes, i)
	}
	// Keep provider prompts deterministic regardless of map iteration order.
	sort.Ints(report.InvalidIndexes)
	ordered := report.InvalidIndexes[:0]
	for i := range plan.Segments {
		if _, ok := invalid[i]; ok {
			ordered = append(ordered, i)
		}
	}
	report.InvalidIndexes = ordered
	report.Valid = len(report.InvalidIndexes) == 0 && report.Valid
	return report
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func splitGeneratedSegmentParagraphs(text string) []string {
	cleaned := strings.TrimSpace(SanitizeScriptOutput(text))
	if cleaned == "" {
		return nil
	}
	raw := strings.Split(cleaned, "\n\n")
	paragraphs := make([]string, 0, len(raw))
	for _, paragraph := range raw {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
		}
	}
	return paragraphs
}

func frozenSegmentPrompt(base string, plan *scriptpkg.ResolvedGenerationPlan, texts []string, invalid []int, settings segmentValidationSettings) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n[SELECTIVE_SEGMENT_REGENERATION]\n")
	b.WriteString("Valid frozen segments are immutable. Copy them exactly; do not rewrite, shorten, expand, or reorder them.\n")
	for i, text := range texts {
		if containsInt(invalid, i) {
			continue
		}
		fmt.Fprintf(&b, "FROZEN SEGMENT %d:\n%s\n\n", i+1, text)
	}
	b.WriteString("Regenerate ONLY these segment numbers, in this order: ")
	for i, index := range invalid {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", index+1)
	}
	b.WriteString(".\nReturn exactly one prose paragraph per requested segment, separated by one blank line. Do not return frozen segments.\n")
	for _, index := range invalid {
		budget := segmentBudgetFor(plan, index, settings.segmentTolerancePercent)
		segment := plan.Segments[index]
		fmt.Fprintf(&b, "SEGMENT %d: topic=%s; target=%d; minimum=%d; maximum=%d\n", index+1, segment.Topic, budget.Target, budget.Min, budget.Max)
	}
	return b.String()
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func mergeRegeneratedSegments(frozen []string, invalid []int, regenerated []string) []string {
	merged := append([]string(nil), frozen...)
	for i, index := range invalid {
		if i < len(regenerated) {
			merged[index] = strings.TrimSpace(regenerated[i])
		}
	}
	return merged
}

func assembleFrozenSegments(texts []string) string {
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.Join(parts, "\n\n")
}

func (e *Engine) generateSegments(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	req ports.TextGenerationRequest,
) (*ports.GenerationResult, error) {
	if plan == nil || len(plan.Segments) == 0 {
		// ollama.generate is the external LLM boundary. The canonical Run
		// clock records it as an OperationReport under script.engine.
		var out *ports.GenerationResult
		if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     stageScriptEngine,
			Component: kernobs.ComponentOllama,
			Operation: kernobs.OperationGenerate,
		}, func(opCtx context.Context) error {
			var genErr error
			out, genErr = e.ollamaGen.GenerateScript(opCtx, req)
			return genErr
		}); err != nil {
			return nil, err
		}
		return out, nil
	}
	settings := e.segmentSettings()
	texts := make([]string, len(plan.Segments))
	var first *ports.GenerationResult
	for index, segment := range plan.Segments {
		segmentPlan := *plan
		segmentPlan.Segments = []scriptpkg.ScriptSegment{segment}
		segmentPlan.TargetWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		segmentPlan.SegmentWords = 0
		if plan.ClipEvidence != nil && index < len(plan.ClipEvidence.SegmentEvidence) {
			segmentPlan.ClipEvidence = scriptpkg.NewClipEvidence(*plan.ClipEvidence)
			segmentPlan.ClipEvidence.SegmentEvidence = []scriptpkg.SegmentClipEvidence{plan.ClipEvidence.SegmentEvidence[index]}
		}
		segmentReq := req
		segmentReq.Prompt = buildSegmentInstructions(&segmentPlan) + "\n\n" + plainTextInstruction
		segmentReq.SourceText = strings.TrimSpace(segment.SourceText)
		segmentReq.ClipIDs = append([]string(nil), segment.ClipIDs...)
		segmentReq.MinWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		var result *ports.GenerationResult
		for attempt := 0; attempt <= settings.maxRegenerationAttempts; attempt++ {
			// Each per-segment LLM call is its own ollama.generate operation.
			if opErr := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
				Stage:     stageScriptEngine,
				Component: kernobs.ComponentOllama,
				Operation: kernobs.OperationGenerate,
			}, func(opCtx context.Context) error {
				var genErr error
				result, genErr = e.ollamaGen.GenerateScript(opCtx, segmentReq)
				return genErr
			}); opErr != nil {
				return nil, opErr
			}
			candidate := splitGeneratedSegmentParagraphs(result.Script)
			// This request owns exactly one editorial segment. Models may
			// still insert an internal blank line; collapse those fragments
			// into the one deterministic paragraph for this segment. The
			// fragments cannot contain another segment because the request
			// carries only this segment's evidence.
			if len(candidate) > 1 {
				candidate = []string{strings.Join(candidate, " ")}
			}
			if len(candidate) == 1 {
				texts[index] = candidate[0]
				singleReport := validateSegmentTexts(&segmentPlan, candidate, settings)
				if singleReport.Valid {
					break
				}
			}
			if attempt == settings.maxRegenerationAttempts {
				return nil, fmt.Errorf("%w: segment[%d] did not produce one valid paragraph", scriptpkg.ErrSegmentValidationFailed, index)
			}
			segmentReq.Prompt += fmt.Sprintf("\n\nRegenerate only this segment. Target %d words; return exactly one paragraph.", segmentReq.MinWords)
		}
		if first == nil {
			first = result
		}
	}
	if first == nil {
		return nil, fmt.Errorf("%w: no segments generated", scriptpkg.ErrSegmentValidationFailed)
	}
	if report := validateSegmentTexts(plan, texts, settings); !report.Valid {
		return nil, fmt.Errorf("%w: %s", scriptpkg.ErrSegmentValidationFailed, strings.Join(report.Reasons, "; "))
	}
	frozenText := assembleFrozenSegments(texts)
	result := *first
	result.Script = frozenText
	result.WordCount = textutil.CountWords(frozenText)
	return &result, nil
}
