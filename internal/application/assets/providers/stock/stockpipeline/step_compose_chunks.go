// Package stockpipeline — step_compose_chunks.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockComposeChunksStep — the canonical
// implementation of the stock.compose_chunks step (Step 4 of the
// 6-step pipeline) per godlike/06 SSOT. P6 (July 2026): wired
// with real StockRenderer.Render — each cut path is rendered
// individually (one clip → one composed chunk) preserving the
// 1:1 cardinality established by the Commit 5 stub.
//
// godlike/07 fail-closed contracts:
//   - cutPaths empty → Debug + return nil (no work to do).
//   - renderer.Render returns err → graceful degradation (Warn +
//     continue); the all-failed case is caught by
//     ErrStockComposeChunksAllFailed at the end of the step so
//     the orchestrator surfaces it as a job failure.
//
// Per-chunk failures degrade gracefully (Warn + continue); the
// all-failed case is caught by ErrStockComposeChunksAllFailed
// at the end of the step so the orchestrator surfaces it as a
// job failure (NOT as a job success with zero composed chunks).
// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (Wave 1 P0 #2, 2026-07-04).
// Do NOT revert the per-chunk `continue` to `return fmt.Errorf`
// — that would reintroduce the godlike/07 no-fake-availability
// regression where a single bad chunk tanked the whole compose.
package stockpipeline

import (
	"context"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
)

// StockComposeChunksStep is the canonical implementation of
// stock.compose_chunks. P6 (July 2026): wired with real
// StockRenderer.Render — each cut path is rendered individually
// (one clip → one composed chunk) preserving the 1:1 cardinality
// established by the Commit 5 stub.
type StockComposeChunksStep struct{}

func (StockComposeChunksStep) Name() string { return StepKeyStockComposeChunks }

func (StockComposeChunksStep) Run(ctx context.Context, runner StepRunner) (err error) {
	phaseMetric := startStockPhase(ctx, runner, "stock.compose")
	defer func() {
		if phaseMetric != nil {
			cutPaths := runner.State().CutPaths
			composed := runner.State().ComposedPaths
			phaseMetric.SetItems(int64(len(cutPaths)), int64(len(composed)))
			phaseMetric.SetItemsFailed(int64(len(cutPaths) - len(composed)))
		}
		finishStockPhase(runner, phaseMetric, "stock.compose", err)
	}()

	cutPaths := runner.State().CutPaths

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.compose_chunks: starting",
			zap.Int("cut_paths", len(cutPaths)))
	}

	if len(cutPaths) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.compose_chunks: empty cut paths — nothing to compose")
		}
		return nil
	}

	// A cutter output with neither effects nor transitions is already
	// normalized to the canonical stock profile. Do not invoke the renderer
	// for a second encode; pass the verified cut artifacts through as the
	// composed/final input consumed by stock.publish.
	in := runner.RunInput()
	if isCanonicalFinalCut(in) {
		runner.State().ComposedPaths = append([]string(nil), cutPaths...)
		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.compose_chunks: bypassed — cutter output is canonical final artifact",
				zap.Int("final_paths", len(cutPaths)))
		}
		return nil
	}

	// godlike/07 composition-time guarantee (PR-STOCK-PRODUCTION-DEPS,
	// July 2026): runner.Renderer() is non-nil. The canonical
	// composition root (NewProductionStockPipeline + orchestrator.RunResilient)
	// rejects nil renderer with ErrStockPipelineNilRenderer /
	// ErrOrchestratorNilDeps BEFORE the step body runs. The previous
	// runtime nil-check (test-fixture compat) is RETIRED per
	// godlike/07 no-fake-availability: a production run cannot reach
	// here with a nil renderer, and a test fixture that passes nil
	// must update to wire a non-nil stub (mapRenderer).
	renderer := runner.Renderer()

	noAudio := in != nil && in.NoAudio
	noTransitions := in != nil && in.NoTransitions
	noEffects := in != nil && in.NoEffects

	cfg := runner.Cfg()
	clipDur := cfg.ClipDurationSec
	if clipDur <= 0 {
		clipDur = 5
	}

	composed := make([]string, 0, len(cutPaths))
	canonical := DefaultPipelineConfig()
	for i, cutPath := range cutPaths {
		outputPath := filepath.Join("/tmp",
			fmt.Sprintf("stock_composed_%s_%d.mp4", runner.JobID(), i))

		req := RenderRequest{
			OutputPath:       outputPath,
			InputPaths:       []string{cutPath},
			Width:            canonical.Width,
			Height:           canonical.Height,
			FPS:              canonical.FPS,
			Codec:            "",
			Preset:           canonical.Preset,
			CRF:              canonical.CRF,
			KeyframeInterval: canonical.KeyframeInterval,
			KeepAudio:        !noAudio,
			NoTransitions:    noTransitions,
			NoEffects:        noEffects,
			TransitionEvery:  1,
			ClipDurationSec:  clipDur,
			EffectsDir:       DefaultPipelineConfig().EffectsDir,
			EffectEvery:      DefaultPipelineConfig().EffectInterval,
			EffectIndexHint:  i,
			Logger:           runner.Log(),
			ChunkIndex:       i,
		}

		resolved, resolveErr := ResolveRenderPlan(req)
		if resolveErr != nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.compose_chunks: render plan resolution failed",
					zap.Int("chunk_index", i), zap.Error(resolveErr))
			}
			continue
		}
		_, renderErr := renderer.Render(ctx, resolved)
		if renderErr != nil {
			// PR-STOCK-FAKE-AVAILABILITY-REMOVAL (2026-07-04): per-chunk
			// graceful degradation (Warn + continue) mirrors the
			// StockStageSourcesStep pattern — a single bad chunk
			// shouldn't tank the whole compose. The fail-closed
			// gate at the end of the step catches the all-failed
			// case via ErrStockComposeChunksAllFailed so the
			// orchestrator surfaces it as a job failure.
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.compose_chunks: Render chunk failed — graceful degradation",
					zap.Int("chunk_index", i), zap.Error(renderErr))
			}
			continue
		}
		composed = append(composed, outputPath)

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.compose_chunks: rendered chunk",
				zap.Int("chunk_index", i),
				zap.String("output", outputPath))
		}
	}

	// Fail-closed gate (PR-STOCK-FAKE-AVAILABILITY-REMOVAL, 2026-07-04):
	// if the renderer was wired (non-nil check above) AND we had cut
	// paths (len(cutPaths) > 0 check above) AND every chunk failed to
	// render (zero string paths appended to the composed slice), surface
	// ErrStockComposeChunksAllFailed as a job failure. Mirrors the
	// StockStageSourcesStep fail-closed pattern. The per-chunk graceful
	// degradation (Warn + continue on err) is preserved above so
	// partial successes still produce partial artifacts.
	if len(composed) == 0 {
		return ErrStockComposeChunksAllFailed
	}

	runner.State().ComposedPaths = composed

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.compose_chunks: SUCCEEDED",
			zap.Int("composed_count", len(composed)),
			zap.Int("cut_paths", len(cutPaths)))
	}
	return nil
}
