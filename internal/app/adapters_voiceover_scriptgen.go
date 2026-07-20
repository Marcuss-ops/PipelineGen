// Package app — adapters_voiceover_scriptgen.go implements
// scriptgeneration.VoiceoverGenerator by wrapping the canonical
// TTS audio processor.
//
// Verdetto P1: the VoiceoverGenerator produces audio assets from
// scene text (real TTS generation, not just copying existing
// voiceover_paths / audio_path fields). It runs AFTER translation
// and produces one voiceover per scene per language.
//
// Architecture:
//
//	scriptgeneration.VoiceoverGenerator
//	         ↑ implements
//	app.ScriptVoiceoverGenerator
//	         ↑ wraps
//	audioasset.Processor → scripts/bridges/tts_edge.py
//
// This adapter is intentionally simple: it takes a single text +
// language, calls the TTS engine, and returns an AudioReference
// with the local file path. Upload to Drive (if needed) is handled
// by a separate stage in the runner pipeline.
package app

import (
	"context"
	"fmt"
	"strings"

	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration"

	"go.uber.org/zap"
)

// ScriptVoiceoverGenerator implements scriptgeneration.VoiceoverGenerator
// by wrapping the canonical TTS audio processor. Each call to Generate
// produces a real audio file via the TTS pipeline.
//
// Panics on nil processor (fail-fast per godlike/07 NO-FAKE-AVAILABILITY).
type ScriptVoiceoverGenerator struct {
	proc   *audioasset.Processor
	outDir string
	log    *zap.Logger
}

// NewScriptVoiceoverGenerator constructs the ScriptVoiceoverGenerator.
// proc and outDir are required; nil/empty panics at construction time.
// log is nil-safe (replaced with zap.NewNop()).
func NewScriptVoiceoverGenerator(proc *audioasset.Processor, outDir string, log *zap.Logger) *ScriptVoiceoverGenerator {
	if proc == nil {
		panic("app: ScriptVoiceoverGenerator requires a non-nil audio processor")
	}
	if outDir == "" {
		panic("app: ScriptVoiceoverGenerator requires a non-empty output directory")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ScriptVoiceoverGenerator{
		proc:   proc,
		outDir: outDir,
		log:    log,
	}
}

// Generate implements scriptgeneration.VoiceoverGenerator.
// It produces a TTS audio file for the given scene text + language
// and returns an AudioReference pointing to the local file.
//
// The filename is deterministic: scene_{sceneID}_{language}.mp3 so
// retries produce the same file (idempotent local write).
func (g *ScriptVoiceoverGenerator) Generate(
	ctx context.Context,
	input scriptgen.VoiceoverInput,
) (scriptgen.AudioReference, error) {
	if input.Text == "" {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: empty text for scene %s language %s", input.SceneID, input.Language)
	}
	if input.SceneID == "" {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: empty scene ID")
	}

	// Build a safe filename: scene_{sanitizedSceneID}_{language}.mp3
	safeSceneID := sanitizeFilename(input.SceneID)
	filename := fmt.Sprintf("scene_%s_%s.mp3", safeSceneID, string(input.Language))

	g.log.Info("voiceover scriptgen: generating TTS",
		zap.String("scene_id", input.SceneID),
		zap.String("language", string(input.Language)),
		zap.String("filename", filename),
		zap.String("text_preview", truncateText(input.Text, 80)),
	)

	result, err := g.proc.Generate(ctx, &audioasset.AudioInput{
		Text:          input.Text,
		Language:      string(input.Language),
		Filename:      filename,
		OutputDir:     g.outDir,
		RemoveSilence: true, // strip silence from generated audio
		// UseStdin stays false — text is passed via --text flag.
		// For very long text (>32KB) the processor's legacy path
		// falls back to stdin automatically.
	})
	if err != nil {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: TTS generation for scene %s language %s failed: %w",
			input.SceneID, input.Language, err)
	}

	if result.LocalPath == "" {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: TTS returned empty LocalPath for scene %s language %s",
			input.SceneID, input.Language)
	}

	ref := scriptgen.AudioReference{
		ID:       result.FileHash,
		FilePath: result.LocalPath,
		Duration: 0, // Duration is not returned by the current processor — set to 0
	}

	g.log.Info("voiceover scriptgen: TTS generated",
		zap.String("scene_id", input.SceneID),
		zap.String("language", string(input.Language)),
		zap.String("file_path", result.LocalPath),
		zap.String("file_hash", result.FileHash),
	)

	return ref, nil
}

// sanitizeFilename replaces characters unsafe for filenames with
// underscores. Keeps the result short enough for filesystem limits.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		" ", "_",
		":", "_",
		"..", "_",
	)
	safe := replacer.Replace(name)
	// Keep it reasonably short (50 chars max) to avoid filesystem issues.
	if len(safe) > 50 {
		safe = safe[:50]
	}
	// Trim trailing/leading underscores and dots.
	safe = strings.Trim(safe, "_.")
	if safe == "" {
		safe = "unnamed"
	}
	return safe
}

// truncateText returns the first n characters of text for logging.
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

// Compile-time assertion: *ScriptVoiceoverGenerator implements
// scriptgeneration.VoiceoverGenerator. Drift in the Generate signature
// triggers a build error here (godlike/06 Pattern 0).
var _ scriptgen.VoiceoverGenerator = (*ScriptVoiceoverGenerator)(nil)
