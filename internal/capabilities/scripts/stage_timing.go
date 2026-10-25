// Package scriptgeneration — stage_timing.go owns the canonical observability
// STAGE names for the single-item script-generation pipeline.
//
// These names are the STAGE dimension (business phase boundaries) and are
// ALIASES of the canonical constants in internal/kernel/observability — the
// registry is the single owner of every phase literal (see registry.go). The
// external technical calls (qdrant.search, sqlite.hydrate, ollama.generate,
// drive.upload, ...) are recorded separately as OperationReport
// observations so a phase wall time is never confused with an accumulated
// dependency time. Every stage below is measured with MeasureStageReport —
// the single canonical clock owned by internal/kernel/observability — and
// must never be re-measured with an ad-hoc time.Now().
//
// The file also owns the RUNNER PHASE aliases (generate, voiceover,
// audio_compile, translation, persistence) and measurePhase, the one
// wrapper that records a phase on the canonical Run clock. The two sets are one
// ownership surface: a stage a report joins against and the measurement that
// writes it must never be declared in different files, or a renamed phase
// silently reports under a label nothing joins on. Merged 2026-10-10 from
// runner_phase_timing.go for that reason (and to stay inside the
// max_lines_per_file_strict budget of the package).
package scriptgeneration

import (
	"context"
	"errors"

	"go.uber.org/zap"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const (
	StageScriptPrepare     = kernobs.StageScriptPrepare
	StageScriptNormalize   = kernobs.StageScriptNormalize
	StageScriptValidate    = kernobs.StageScriptValidate
	StageSourceResolve     = kernobs.StageSourceResolve
	StageScriptPlan        = kernobs.StageScriptPlan
	StageScriptEngine      = kernobs.StageScriptEngine
	StageScriptPostprocess = kernobs.StageScriptPostprocess
	StageAudioPipeline     = kernobs.StageAudioPipeline
	// StageSceneAnalysis is the business stage under which per-scene
	// entity/phrase/word extraction is recorded as nlp.extract operations.
	StageSceneAnalysis = kernobs.StageSceneAnalysis
	// StageOverlayPrepare is the stage under which the overlay.prepare job
	// enqueue (submitted before TTS) is recorded.
	StageOverlayPrepare = kernobs.StageOverlayPrepare
	// StageOverlayRender is the stage under which the BLOCKING overlay render
	// (submit + wait for completion + publish + RenderingGen phase projection)
	// is recorded.
	//
	// It exists because that render was sequenced inside the audio phase and
	// therefore charged to `audio_compile`: the audio stage reported a wall
	// time dominated by a video render it does not own, the render never
	// appeared on the critical path, and the run's reported bottleneck was the
	// audio stage with a misleading dominant operation. The render is a
	// distinct business boundary, so it gets its own stage — sibling to the
	// audio stage, never nested inside it, or the breakdown would re-attribute
	// it to the enclosing stage.
	StageOverlayRender = kernobs.StageOverlayRender
	// StageAudioFinalize is the stage under which the canonical
	// EditingTimelineV1 projection and the AUDIO_COMPILE step completion are
	// recorded. It is separate from the compile stage because it runs AFTER the
	// overlay render (the timeline's overlay span carries the certified render
	// artifact), and because audio_compile must report only the work it owns.
	StageAudioFinalize = kernobs.StageAudioFinalize
	// StageAudioPublish is the stage under which the certified final-audio
	// artifact is uploaded to Drive. It is separated from the audio compile
	// stage for the same reason: publishing is IO against an external system,
	// not audio compilation, and merging the two made drive.upload the
	// reported dominant operation of the audio stage.
	StageAudioPublish = kernobs.StageAudioPublish
)

// Canonical-runner phase stage names. These are the pipeline business phases
// owned by the Runner (NOT the generic observability taxonomy, and NOT the
// legacy GenerateOneUseCase stage vocabulary). Each name matches the stage
// label already used by the operations recorded inside that phase, so
// Breakdown/FanoutReports join the phase's wall time with its operations
// deterministically.
//
//   - generate      ← runSceneTextPhase   (ollama.generate is recorded under kernobs.StageGenerate)
//   - voiceover     ← runVoiceoverPhase   (tts.synthesize under voiceoverStage)
//   - audio_compile ← runAudioCompilePhase + publishFinalAudio (audio ops + drive.upload under audioCompileStage)
//
// Aliased from internal/kernel/observability: the registry is the single
// owner of every phase literal, so the runner cannot drift from the taxonomy
// a report is joined against.
const (
	stageNormalize   kernobs.StageName = kernobs.StageRunNormalize
	stageTranslation kernobs.StageName = kernobs.StageRunTranslation
	stagePersistence kernobs.StageName = kernobs.StageRunPersistence
)

// errPhaseFailed marks a phase that returned false inside measurePhase so the
// canonical StageReport is recorded with a failed status (never as a silent
// completed stage).
var errPhaseFailed = errors.New("scriptgeneration: phase failed")

// measurePhase wraps one Runner phase with MeasureStageReport on the canonical
// Run clock (never a second timer). The phase keeps its bool contract: false
// is translated into a typed error so the StageReport status reflects the
// failure, then propagated unchanged to the caller. When no Run is bound to
// ctx, MeasureStageReport degrades to a pass-through and behaviour is
// identical to calling the phase directly.
func (r *Runner) measurePhase(ctx context.Context, stage kernobs.StageName, fn func(context.Context) bool) bool {
	ok := false
	if _, err := kernobs.MeasureStageReport(ctx, stage, func(c context.Context) error {
		ok = fn(c)
		if !ok {
			return errPhaseFailed
		}
		return nil
	}); err != nil && r.log != nil {
		r.log.Warn("script phase stage report failed", zap.String("stage", string(stage)), zap.Error(err))
	}
	return ok
}
