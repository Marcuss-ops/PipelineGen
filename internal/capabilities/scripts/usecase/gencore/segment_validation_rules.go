// Package usecase — segment_validation_rules.go: the pure rules of segment
// validation — the exhausted-fallback classification, the per-segment budget
// math and the validation report.
//
// Nothing here touches the Engine, the network or the clock: the rules are
// pure functions over a plan and its generated texts, so they are directly
// unit-testable and cannot drift from the orchestration that consumes them
// (segment_validation.go).
//
// Extracted 2026-09-12 from segment_validation.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package gencore

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// isSegmentValidationExhausted is the SINGLE classification function for the
// segment-validation fallback path. It classifies exclusively with errors.Is
// against the canonical ErrSegmentValidationFailed sentinel
// (internal/kernel/script) so a reworded message can never silently re-route
// the fallback. String matching on the error text is banned here.
func isSegmentValidationExhausted(err error) bool {
	return errors.Is(err, scriptpkg.ErrSegmentValidationFailed)
}

const (
	defaultSegmentWordsTolerancePercent = 15.0
	defaultTotalWordsTolerancePercent   = 10.0
	// Small local Ollama models have noticeably variable completion lengths.
	// Keep the word gate strict, but allow enough bounded regeneration attempts
	// to obtain a compliant paragraph instead of dead-lettering a valid request
	// after only three samples.
	defaultMaxSegmentRegeneration = 1
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
	if settings.maxRegenerationAttempts < 0 {
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
	// Clip introductions have a natural length and must never be padded with
	// repeated source text just to satisfy a documentary word budget. Their
	// deterministic checks are non-empty output plus the copy/instruction gate
	// performed by generateOne below.
	if plan.ClipEvidence != nil {
		for i, text := range texts {
			budget := segmentBudgetFor(plan, i, settings.segmentTolerancePercent)
			actual := textutil.CountWords(text)
			// Short clip intros may naturally exceed the nominal target.
			// Keep a bounded relaxed ceiling at 2x target for Gemma output.
			maxWords := budget.Max
			if relaxedMax := budget.Target * 2; relaxedMax > maxWords {
				maxWords = relaxedMax
			}
			// Clip narration must stay natural and may be shorter than the
			// generic minimum, but it must still have a hard upper bound. A
			// previous fast path checked only for non-empty output, allowing a
			// small clip intro to expand into hundreds of words and making TTS
			// and rendering disproportionately expensive.
			if strings.TrimSpace(text) == "" || actual > maxWords {
				report.Valid = false
				report.InvalidIndexes = append(report.InvalidIndexes, i)
				if strings.TrimSpace(text) == "" {
					report.Reasons = append(report.Reasons, fmt.Sprintf("segment[%d] produced empty clip introduction", i))
				} else {
					report.Reasons = append(report.Reasons,
						fmt.Sprintf("segment[%d] clip introduction words=%d exceeds max=%d target=%d", i, actual, maxWords, budget.Target))
				}
			}
		}
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
