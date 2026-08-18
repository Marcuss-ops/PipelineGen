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
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// GenerationFinalizer assembles the final GenerationResult and
// runs the quality gate. It is constructed once per use case and
// reused across calls.
type GenerationFinalizer struct {
	log          *zap.Logger
	cfg          adapters.NormalizationConfig
	memSvc       *adapters.Service
	vidRushCache scriptports.VidRushCachePort
}

// NewGenerationFinalizer constructs a GenerationFinalizer.
func NewGenerationFinalizer(log *zap.Logger, cfg adapters.NormalizationConfig) *GenerationFinalizer {
	return &GenerationFinalizer{log: log, cfg: cfg}
}

// SetMemoryService wires the gemmamemory service used to cache
// successfully generated scripts. If nil, caching is a no-op.
func (f *GenerationFinalizer) SetMemoryService(svc *adapters.Service) {
	if f != nil {
		f.memSvc = svc
	}
}

// SetVidRushCache wires the durable binding L2 cache. It is optional for
// compatibility with lightweight/unit-test compositions; provider caches and
// binding correctness remain valid when it is absent.
func (f *GenerationFinalizer) SetVidRushCache(cache scriptports.VidRushCachePort) {
	if f != nil {
		f.vidRushCache = cache
	}
}

// FinalizeInputs carries everything the finalize phase needs from
// the previous pipeline phases.
type FinalizeInputs struct {
	Item         scriptpkg.GenerationItemV2
	Plan         scriptpkg.ResolvedGenerationPlan
	EngineResult *EngineResult
	PostResult   *adapters.PipelineResult
	Provenance   *scriptpkg.GenerationProvenance
}

// Finalize builds the result, enforces the clip-native contract,
// surfaces provenance, and evaluates the editorial quality gate.
func (f *GenerationFinalizer) Finalize(
	ctx context.Context,
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
	result := buildGenerationResultWithCache(item, plan, engineResult, postResult, f.vidRushCache, ctx)
	result.AudioMode = plan.AudioMode

	if err := enforceClipNativeContract(result, item, plan, engineResult, postResult); err != nil {
		return nil, err
	}

	result.Provenance = provenance
	if provenance != nil && (provenance.DocID != "" || provenance.DocLink != "") {
		result.Artifacts.Document = &scriptpkg.DocumentArtifact{
			DocID:   provenance.DocID,
			DocLink: provenance.DocLink,
		}
	}

	qualityInput := *result
	qualityInput.Output = result.Output
	qualityInput.Output.Text = engineResult.Output.Text
	qualityInput.Output.WordCount = engineResult.Output.WordCount
	// Keep the canonical result in sync with the engine envelope before
	// persistence; the script cache must retain the observed word count.
	result.Output.WordCount = engineResult.Output.WordCount
	quality, qErr := evaluateQualityGate(&qualityInput, item, plan)
	if quality != nil {
		result.Quality = quality
	}
	if quality != nil {
		tracker.TrackEvent("quality.checked", "Editorial quality gate checked", map[string]any{
			"item_id":                     item.ID,
			"passed":                      quality.Passed,
			"source_text_coverage":        quality.SourceTextCoverage,
			"source_text_coverage_status": quality.SourceTextCoverageStatus,
			"clip_evidence_coverage":      quality.ClipEvidenceCoverage,
			"unsupported_claims":          quality.UnsupportedClaims,
			"actual_words":                quality.ActualWords,
			"target_words":                quality.TargetWords,
		})
	}
	// Sprint 1.3 (godlike/08): centralize success classification.
	// Order: build → enforce → quality → warnings → classify → emit.
	// The classify step runs here, once, via
	// ClassifyGenerationStatus. qualitySkipped is the only
	// non-result input that influences the canonical Status
	// (warnings are read directly off result.Warnings).
	qualitySkipped := false
	if qErr != nil {
		if item.ScriptParams.SkipQualityGate {
			qualitySkipped = true
			result.Quality.SourceTextCoverageStatus = "SKIPPED"
			tracker.TrackEvent("quality.skipped", "Editorial quality gate failure ignored by request", map[string]any{
				"item_id": item.ID,
				"error":   qErr.Error(),
			})
		} else {
			// Terminal failure path: set the canonical FAILED status
			// directly (NOT via ClassifyGenerationStatus — the verdict
			// mandates a single classify call in the success path).
			result.Status = scriptpkg.ItemStatusFailed
			return result, qErr
		}
	}

	// Cache the generated script so future requests with the same
	// canonical inputs can be served without calling the LLM. We save
	// the RAW engine output (not post-processed translations) so the
	// cached value matches the cache key, which is derived from the
	// generation plan. We save only on the success path (quality
	// passed or explicitly skipped), only when the caller opted into
	// memory. Exact cache hits are not re-persisted; force-refresh
	// generations overwrite the exact row.
	if f.memSvc != nil && plan.UseMemory &&
		engineResult.CacheStatus == "generated" && engineResult.Output.Text != "" {
		_, saveErr := f.memSvc.SaveAfterGeneration(ctx, adapters.SaveGenerationInput{
			ChannelID: "default",
			Mode:      plan.Mode,
			Language:  plan.Language,
			Title:     plan.Title,
			Prompt:    plan.RenderedPrompt,
			Model:     engineResult.Model,
			WordCount: engineResult.Output.WordCount,
			CacheKey:  plan.CacheKey,
		}, engineResult.Output.Text)
		if saveErr != nil && f.log != nil {
			f.log.Warn("generate-one: failed to save script cache",
				zap.String("item_id", item.ID),
				zap.Error(saveErr))
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

	// Sprint 1.3 classify phase (SOLE writer of result.Status in the
	// success path). Order: build → enforce → quality → warnings
	// (already collected above by buildGenerationResult, the
	// clip-native warnings appended by enforceClipNativeContract,
	// and the tracker events emitted by the quality gate) →
	// classify → emit (in generate_one_usecase.go::Execute).
	result.Status = ClassifyGenerationStatus(result, qualitySkipped)
	return result, nil
}
