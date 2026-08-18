package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
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
	generate := func(ctx context.Context, req ports.TextGenerationRequest) (*ports.GenerationResult, error) {
		if e.generationGate != nil {
			if err := e.generationGate.AcquireHigh(ctx); err != nil {
				return nil, err
			}
			defer e.generationGate.Release()
		}
		var out *ports.GenerationResult
		err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     scriptgen.StageScriptEngine,
			Component: kernobs.ComponentOllama,
			Operation: kernobs.OperationGenerate,
		}, func(opCtx context.Context) error {
			var genErr error
			out, genErr = e.ollamaGen.GenerateScript(opCtx, req)
			return genErr
		})
		return out, err
	}
	if plan == nil || len(plan.Segments) == 0 {
		return generate(ctx, req)
	}
	settings := e.segmentSettings()
	texts := make([]string, len(plan.Segments))
	var first *ports.GenerationResult
	type segmentOutput struct {
		index  int
		text   string
		result *ports.GenerationResult
		err    error
	}
	generateOne := func(index int, segment scriptpkg.ScriptSegment) segmentOutput {
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
		// The global source (e.g. the research source text resolved for the
		// whole topic) remains the grounding for a segment that declares no
		// per-segment source_text. The prompt footer promises "use the topic
		// and the global source", so only an explicit per-segment source_text
		// may override it — never an empty value.
		if segSource := strings.TrimSpace(segment.SourceText); segSource != "" {
			segmentReq.SourceText = segSource
		}
		segmentReq.ClipIDs = append([]string(nil), segment.ClipIDs...)
		segmentReq.MinWords = segmentBudgetFor(plan, index, settings.segmentTolerancePercent).Target
		var result *ports.GenerationResult
		var lastErr error
		for attempt := 0; attempt <= settings.maxRegenerationAttempts; attempt++ {
			var genErr error
			result, genErr = generate(ctx, segmentReq)
			if genErr != nil {
				lastErr = genErr
				break
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
				lastErr = fmt.Errorf("%w: segment[%d] did not produce one valid paragraph", scriptpkg.ErrSegmentValidationFailed, index)
				break
			}
			segmentReq.Prompt += fmt.Sprintf("\n\nRegenerate only this segment. Target %d words; return exactly one paragraph.", segmentReq.MinWords)
		}
		if lastErr != nil {
			return segmentOutput{index: index, result: result, err: lastErr}
		}
		return segmentOutput{index: index, text: texts[index], result: result}
	}
	workers := len(plan.Segments)
	if e.generationGate != nil {
		workers = e.generationGate.Capacity()
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(plan.Segments) {
		workers = len(plan.Segments)
	}
	jobs := make(chan int)
	results := make(chan segmentOutput, len(plan.Segments))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- generateOne(index, plan.Segments[index])
			}
		}()
	}
	go func() {
		for index := range plan.Segments {
			select {
			case jobs <- index:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				close(results)
				return
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for output := range results {
		if output.err != nil {
			return nil, output.err
		}
		texts[output.index] = output.text
		if first == nil {
			first = output.result
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
