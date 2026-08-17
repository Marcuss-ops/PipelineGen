package scriptgeneration

import (
	"context"
	"errors"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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
const (
	stageNormalize   = "normalize"
	stageTranslation = "translation"
	stagePersistence = "persistence"
	stageDocument    = "document"
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
	kernobs.MeasureStageReport(ctx, stage, func(c context.Context) error {
		ok = fn(c)
		if !ok {
			return errPhaseFailed
		}
		return nil
	})
	return ok
}
