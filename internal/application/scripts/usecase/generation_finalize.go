// Package usecase — generation_finalize.go owns the canonical
// finalize phase for single-item script generation.
//
// Responsibilities:
//   - build the GenerationResult from engine and postprocessor outputs
//   - enforce the clip-native contract
//   - surface the final provenance block
//   - run the editorial quality gate
//   - emit quality-related tracker events
//
// The finalize phase is stateless and returns a typed
// GenerationResult ready for the orchestrator to attach final
// timings and completion events.
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerationFinalizer assembles the final GenerationResult and
// runs the quality gate. It is constructed once per use case and
// reused across calls.
type GenerationFinalizer struct {
	log *zap.Logger
	cfg adapters.NormalizationConfig
}

// NewGenerationFinalizer constructs a GenerationFinalizer.
func NewGenerationFinalizer(log *zap.Logger, cfg adapters.NormalizationConfig) *GenerationFinalizer {
	return &GenerationFinalizer{log: log, cfg: cfg}
}

// FinalizeInputs carries everything the finalize phase needs from
// the previous pipeline phases.
type FinalizeInputs struct {
	Item         scriptpkg.GenerationItemV2
	Plan         scriptpkg.ResolvedGenerationPlan
	EngineResult *EngineResult
	PostResult   *adapters.PipelineResult
	Provenance   *scriptpkg.GenerationProvenance
	Timings      scriptpkg.GenerationTimings
}

// Finalize builds the result, enforces the clip-native contract,
// surfaces provenance, and evaluates the editorial quality gate.
func (f *GenerationFinalizer) Finalize(
	_ context.Context,
	inputs FinalizeInputs,
	tracker *ProgressTracker,
) (*scriptpkg.GenerationResult, error) {
	if f == nil {
		return nil, fmt.Errorf("finalizer not configured")
	}
	if inputs.EngineResult == nil {
		return nil, fmt.Errorf("engine result is nil")
	}

	item := inputs.Item
	plan := inputs.Plan
	engineResult := inputs.EngineResult
	postResult := inputs.PostResult
	provenance := inputs.Provenance
	timings := inputs.Timings

	result := buildGenerationResult(item, plan, engineResult, postResult, timings)

	if err := enforceClipNativeContract(result, item, plan, engineResult, postResult); err != nil {
		return nil, err
	}

	result.Provenance = provenance

	qualityInput := *result
	qualityInput.Output = result.Output
	qualityInput.Output.Text = engineResult.Output.Text
	qualityInput.Output.WordCount = engineResult.Output.WordCount
	quality, qErr := evaluateQualityGate(&qualityInput, item, plan)
	if quality != nil {
		result.Quality = quality
	}
	if quality != nil {
		tracker.TrackEvent("quality.checked", "Editorial quality gate checked", map[string]any{
			"item_id":                item.ID,
			"passed":                 quality.Passed,
			"source_text_coverage":   quality.SourceTextCoverage,
			"clip_evidence_coverage": quality.ClipEvidenceCoverage,
			"unsupported_claims":     quality.UnsupportedClaims,
			"actual_words":           quality.ActualWords,
			"target_words":           quality.TargetWords,
		})
	}
	if qErr != nil {
		if item.ScriptParams.SkipQualityGate {
			tracker.TrackEvent("quality.skipped", "Editorial quality gate failure ignored by request", map[string]any{
				"item_id": item.ID,
				"error":   qErr.Error(),
			})
		} else {
			result.Status = "FAILED_QUALITY_GATE"
			return result, qErr
		}
	}

	// Log source text metrics once at completion. The raw source text
	// is never logged; only hash, length, token estimate and an
	// optional preview are emitted.
	if f.log != nil {
		f.log.Info("generate-one: source text metrics",
			zap.String("item_id", item.ID),
			zap.Any("source_text", SourceTextLogFields(plan.SourceText, f.cfg)))
	}

	return result, nil
}
