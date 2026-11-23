// Package voiceover — synthesis and audio-post phase extraction
// (BON-08: Execute decomposition).
//
// synthesizeStage and postProcessStage own the two provider-facing
// stages that used to be inline in ProcessSegmentUseCase.Execute:
//
//	Stage 1: TTSProvider.Synthesize (never asked to remove silence)
//	Stage 2: optional AudioPostProcessor.Process (silence removal)
//
// The stage methods perform the side effects and lifecycle logging; the
// pure apply* helpers fold their outputs into the canonical result so
// the duration/cleaned-path/codec rules are testable without ports.
package voiceover

import (
	"context"
	"errors"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// synthesizeStage runs Stage 1 TTS synthesis. It returns the raw
// provider error so the caller owns the result envelope and the typed
// PipelineError classification. Lifecycle logging and the TTS failure
// counter stay attached to the stage that knows its outcome.
func (u *ProcessSegmentUseCase) synthesizeStage(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	timingPolicy audio.TimingRequest,
	log *zap.Logger,
) (TTSOutput, error) {
	emitTTS := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "tts", string(cmd.Language))
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.started", cmd, "", "", 0, 0)
	}

	var ttsOut TTSOutput
	err := kernobs.MeasureStage(ctx, canonicalStageTTS, func(stageCtx context.Context) error {
		var synthErr error
		ttsOut, synthErr = u.deps.TTSProvider.Synthesize(stageCtx, TTSInput{
			Text:      cmd.Text,
			Language:  cmd.Language,
			Voice:     cmd.Voice,
			Rate:      cmd.TTSRate,
			Filename:  cmd.Filename,
			OutputDir: cmd.Dest.FolderPath,
			// P0.2 Fase 2c: AudioPostProcessor owns silence removal, so the
			// provider must never strip it inline (double-processing risk).
			RemoveSilence: false,
			// The provider returns RAW boundaries; the canonical artifact is
			// built later from the final audio.
			Timing: cmd.Timing,
		})
		return synthErr
	})
	if err != nil {
		emitTTS("failed")
		observability.TTSFailuresTotal.Inc()
		return ttsOut, err
	}
	emitTTS("completed")
	if timingPolicy.Mode != audio.TimingDisabled {
		timingEvent(log, "voiceover.timing.capture.completed", cmd, ttsOut.Provider, ttsOut.BoundaryMode, len(ttsOut.WordBoundaries), ttsOut.Duration.Microseconds())
	}
	return ttsOut, nil
}

// postProcessStage runs Stage 2 silence removal when requested and the
// synthesizer produced a local file. The raw provider file is removed
// after a successful clean so only the canonical cleaned asset survives.
func (u *ProcessSegmentUseCase) postProcessStage(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	ttsOut TTSOutput,
	log *zap.Logger,
) (*AudioPostOutput, error) {
	if !cmd.RemoveSilence || u.deps.AudioPostProcessor == nil || ttsOut.LocalPath == "" {
		return nil, nil
	}

	emitPost := stageLog(log, cmd.RequestID, cmd.ID, cmd.Project, "audio_post", string(cmd.Language))
	var postOut AudioPostOutput
	err := kernobs.MeasureStage(ctx, canonicalStageAudioPost, func(stageCtx context.Context) error {
		var postErr error
		postOut, postErr = u.deps.AudioPostProcessor.Process(stageCtx, AudioPostInput{
			LocalPath: ttsOut.LocalPath,
			OutputDir: cmd.Dest.FolderPath,
			Filename:  cmd.Filename,
		})
		return postErr
	})
	if err != nil {
		emitPost("failed")
		return nil, err
	}
	emitPost("completed")

	if ttsOut.LocalPath != "" && postOut.CleanedPath != "" && ttsOut.LocalPath != postOut.CleanedPath {
		// The raw Edge file is no longer needed after post-processing;
		// keep only the cleaned asset that is uploaded and timed.
		if removeErr := os.Remove(ttsOut.LocalPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			log.Warn("voiceover: raw Edge TTS cleanup failed", zap.String("path", ttsOut.LocalPath), zap.Error(removeErr))
		}
	}
	return &postOut, nil
}

// applySynthesisOutput folds the provider output into the result.
func applySynthesisOutput(out *VoiceoverItemResult, ttsOut TTSOutput) {
	if out == nil {
		return
	}
	out.LocalPath = ttsOut.LocalPath
	out.CleanedPath = ttsOut.CleanedPath
	out.DurationMs = ttsOut.Duration.Milliseconds()
	if ttsOut.Voice != "" {
		out.Voice = ttsOut.Voice
	}
	out.ContentSHA256 = ttsOut.ContentSHA256
}

// applyAudioPostOutput folds the cleaned-audio output into the result.
// The cleaned file and duration replace the pre-clean values because the
// timeline must use the bytes that are actually published. It returns
// the silence-removal summary for the structured lifecycle event.
func applyAudioPostOutput(out *VoiceoverItemResult, ttsOut TTSOutput, post *AudioPostOutput) *SilenceCleanupReport {
	if out == nil || post == nil {
		return nil
	}
	if post.CleanedPath != "" {
		out.CleanedPath = post.CleanedPath
	}
	if post.ContentSHA256 != "" {
		out.ContentSHA256 = post.ContentSHA256
	}
	if post.DurationUS > 0 {
		out.DurationMs = (post.DurationUS + 999) / 1000
	}
	return BuildSilenceCleanupReport(ttsOut.Duration.Microseconds(), post.DurationUS, post.EditMap)
}

// hasLocalPayload reports whether synthesis or post-processing produced
// an artefact the publish/finalize stages can consume.
func hasLocalPayload(out *VoiceoverItemResult) bool {
	return out != nil && (out.LocalPath != "" || out.CleanedPath != "")
}
