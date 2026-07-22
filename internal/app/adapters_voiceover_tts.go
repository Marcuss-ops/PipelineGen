// Package app — voiceover TTSProvider + AudioPostProcessor adapters
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Capability cluster: AUDIO synthesis (text→speech via *audioasset.Processor)
// + AUDIO post-processing (silence-removal via pkg/ffmpeg.RemoveSilence).
//
// Both adapters satisfy Pattern 0 narrow ports declared in
// internal/application/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *audio
// imports.
//
// TTSProvider                   ← *audioasset.Processor
// AudioPostProcessor            ← pkg-level ffmpeg.RemoveSilence closure
//
// Fail-closed: nil proc panics at construction (fail-fast per
// AGENTS.md WireUp pattern). The AudioPostProcessor constructor is
// non-panicking (nil-safe log field is acceptable).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────
// TTSProvider adapter.
//
// Bridges *audioasset.Processor → voiceover.TTSProvider. The use case
// only ever supplies well-formed inputs (no path-traversal payloads
// past cmd.Validate), so the lower-level AudioInput fields EscapeHook
// semantics — UseStdin defaults to false. AudioResult carries
// LocalPath + CleanedPath + Voice + FileHash which map 1-a-1 to
// TTSOutput.
// ─────────────────────────────────────────────────────────────────────

type useCaseTTSAdapter struct {
	proc *audioasset.Processor
}

func newUseCaseTTSAdapter(proc *audioasset.Processor) *useCaseTTSAdapter {
	if proc == nil {
		panic("app.adapters_voiceover_use_case: newUseCaseTTSAdapter: proc is required (*audioasset.Processor)")
	}
	return &useCaseTTSAdapter{proc: proc}
}

func (a *useCaseTTSAdapter) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	res, err := a.proc.Generate(ctx, &audioasset.AudioInput{
		Text: in.Text,
		// PR-VO-TYPED-PRIMITIVES (July 2026): in.Language is the
		// typed voiceover.Language envelope. The cross-package seam
		// converts to the raw string for audioasset.AudioInput
		// (the infrastructure layer stays un-typed per the audit
		// scope discipline).
		Language:      string(in.Language),
		Voice:         in.Voice,
		Filename:      in.Filename,
		OutputDir:     in.OutputDir,
		RemoveSilence: in.RemoveSilence,
		// UseStdin defaults to false: the use case path delivers
		// bounded, validated text inputs (POST /generate path →
		// command.Validate path-traversal rejection before field
		// access, mirrors TestGenerateBatch_RejectsPathTraversalPayload).
	})
	if err != nil {
		return voiceover.TTSOutput{}, err
	}
	return voiceover.TTSOutput{
		LocalPath:   res.LocalPath,
		CleanedPath: res.CleanedPath,
		Voice:       res.Voice,
		FileHash:    res.FileHash,
		Duration:    res.Duration,
	}, nil
}

var _ voiceover.TTSProvider = (*useCaseTTSAdapter)(nil)

// ─────────────────────────────────────────────────────────────────────
// AudioPostProcessor adapter.
//
// Implements voiceover.AudioPostProcessor.Process by wrapping the
// package-level ffmpeg.RemoveSilence closure. The cleaned-path
// convention is deterministic: <OutputDir>/cleaned_<Filename> so
// filename uniqueness rules (P1.3) keep the call surface predictable.
// Nil-safe at the use case boundary (only invoked when
// cmd.RemoveSilence == true).
// ─────────────────────────────────────────────────────────────────────

type useCaseAudioAdapter struct {
	log *zap.Logger
}

func newUseCaseAudioAdapter(log *zap.Logger) *useCaseAudioAdapter {
	return &useCaseAudioAdapter{log: log}
}

func (a *useCaseAudioAdapter) Process(ctx context.Context, in voiceover.AudioPostInput) (voiceover.AudioPostOutput, error) {
	if in.LocalPath == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty LocalPath (use case passed a missing local path)")
	}
	if in.OutputDir == "" || in.Filename == "" {
		return voiceover.AudioPostOutput{}, fmt.Errorf("voiceover.audio_post: empty OutputDir/Filename (use case contract violation)")
	}
	// clean file goes to <OutputDir>/cleaned_<basename>; matches the
	// canonical convention used by audioasset.Processor (processor.go:103).
	cleaned := in.OutputDir + "/cleaned_" + in.Filename
	if a.log != nil {
		a.log.Info("voiceover.audio_post: stripping silence",
			zap.String("input", in.LocalPath),
			zap.String("output_dir", in.OutputDir),
			zap.String("filename", in.Filename))
	}
	if err := ffmpeg.RemoveSilence(ctx, "", in.LocalPath, cleaned); err != nil {
		if a.log != nil {
			a.log.Warn("voiceover.audio_post: RemoveSilence failed (caller decides whether to fail-fast)",
				zap.String("input", in.LocalPath),
				zap.String("output", cleaned),
				zap.Error(err))
		}
		return voiceover.AudioPostOutput{}, err
	}
	return voiceover.AudioPostOutput{CleanedPath: cleaned}, nil
}

var _ voiceover.AudioPostProcessor = (*useCaseAudioAdapter)(nil)
