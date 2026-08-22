// Package app — adapters_voiceover_scriptgen.go implements
// scriptgeneration.VoiceoverGenerator by wrapping the canonical per-item
// voiceover pipeline (voiceover.VoiceoverItemExecutor).
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
//	voiceover.VoiceoverItemExecutor (ProcessVoiceoverItemUseCase)
//	         ↑ delegates to
//	voiceover.ProcessSegmentUseCase (canonical 4-stage pipeline)
//
// Routing through the canonical per-item pipeline (instead of calling
// *audioasset.Processor directly) is what lets the adapter return the
// full SpeechTimingArtifact: the use case's TTS stage captures the Edge
// WordBoundary stream, and its publish stage builds the hash-bound
// canonical artifact (timing.json SSOT + optional SRT/VTT). The adapter
// maps that artifact onto AudioReference.Timing so the runner can derive
// the phrase→timestamp projection from the same bytes the audio came from.
package wiring

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// ScriptVoiceoverGenerator implements scriptgeneration.VoiceoverGenerator
// by wrapping the canonical per-item voiceover pipeline. Each call to
// Generate produces a real audio file + (when timing capture succeeds)
// the canonical SpeechTimingArtifact via the shared pipeline.
//
// Panics on nil executor (fail-fast per godlike/07 NO-FAKE-AVAILABILITY).
type ScriptVoiceoverGenerator struct {
	Exec   voiceover.VoiceoverItemExecutor
	OutDir string
	Log    *zap.Logger
}

// NewScriptVoiceoverGenerator constructs the ScriptVoiceoverGenerator.
// exec and outDir are required; nil/empty panics at construction time.
// log is nil-safe (replaced with zap.NewNop()).
func NewScriptVoiceoverGenerator(exec voiceover.VoiceoverItemExecutor, outDir string, log *zap.Logger) *ScriptVoiceoverGenerator {
	if exec == nil {
		panic("app: ScriptVoiceoverGenerator requires a non-nil VoiceoverItemExecutor")
	}
	if outDir == "" {
		panic("app: ScriptVoiceoverGenerator requires a non-empty output directory")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ScriptVoiceoverGenerator{
		Exec:   exec,
		OutDir: outDir,
		Log:    log,
	}
}

// Generate implements scriptgeneration.VoiceoverGenerator.
// It routes the scene text through the canonical per-item voiceover
// pipeline and returns an AudioReference pointing at the produced audio,
// carrying the canonical SpeechTimingArtifact when timing was captured.
//
// The filename is deterministic: scene_{sceneID}_{language}.mp3 so
// retries produce the same file (idempotent local write). Silence removal
// is intentionally disabled: the Edge boundaries describe the exact bytes
// produced, so trimming silence without an edit map would leave stale
// timestamps (the SILENCE gate). The combined-audio stage owns any
// silence/padding normalization on the final timeline.
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

	// Build a safe, JOB-UNIQUE filename:
	// scene_{sanitizedProject}_{sanitizedSceneID}_{language}.mp3.
	// Scene IDs are per-job sequential ("scene-0", "scene-1", …) and
	// concurrent script.generate jobs share one voiceover output directory,
	// so scene+language alone collides across jobs (observed: two jobs both
	// writing scene_scene-0_it.mp3 → interleaved writes corrupt the audio and
	// its metadata.jsonl). The semantic project namespace makes the filename
	// deterministic for retries AND unique per job. Empty Project keeps the
	// legacy scene+language shape (back-compat for direct callers).
	safeSceneID := sanitizeFilename(input.SceneID)
	stem := safeSceneID
	if project := strings.TrimSpace(input.Project); project != "" {
		stem = sanitizeFilename(project) + "_" + safeSceneID
	}
	filename := fmt.Sprintf("scene_%s_%s.mp3", stem, string(input.Language))

	// Timing policy: canonical defaults (best_effort / word / [json]) when
	// the caller did not supply one. Capture is never implicitly mandatory.
	timing := input.Timing
	if timing == nil {
		def := capabilityaudio.DefaultTimingRequest()
		timing = &def
	}

	cmd := &voiceover.GenerateVoiceoverItemCommand{
		// The runner has its own execution lineage; the voiceover per-item
		// command only needs non-empty correlation fields. Scene-derived
		// values keep them deterministic and retry-safe.
		ParentJobID: "scriptgen",
		RequestID:   fmt.Sprintf("scriptgen_%s_%s", stem, string(input.Language)),
		Text:        input.Text,
		Language:    voiceover.Language(input.Language),
		Filename:    filename,
		TextHash:    voiceover.ComputeTextHash(input.Text),
		Timing:      timing,
		// Project is the canonical semantic project namespace resolved ONCE
		// by BuildGenerateRequest and forwarded verbatim so the per-item
		// pipeline reaches VoiceoverPublishCommand.Project (which the
		// publisher requires). Dropping it here was the P0 routing gap: the
		// publisher failed AFTER TTS work was already spent. Empty Project
		// is unreachable here because the runner fails the run before the
		// voiceover phase when it is missing.
		Project: input.Project,
		// No silence trim here — the raw Edge boundaries describe exactly
		// the bytes produced. Trimming without an edit map would produce
		// stale timestamps, which the SILENCE gate forbids.
		RemoveSilence: false,
		Strategy:      asset.StrategyVerify,
	}
	// Destination is the caller-explicit voiceover folder threaded verbatim
	// from output.voiceover_folder_id through the routing context. When
	// present it is marked explicit so neither the group resolver nor the
	// configured default can replace it downstream; nil (empty folder) means
	// the per-item pipeline resolves the configured voiceover folder via its
	// DefaultFolderResolver.
	if folderID := strings.TrimSpace(input.VoiceoverFolderID); folderID != "" {
		cmd.Destination = &voiceover.DestinationRequest{
			Kind:     string(voiceover.KindExplicit),
			FolderID: folderID,
			Project:  input.Project,
		}
	}

	g.Log.Info("voiceover scriptgen: generating TTS",
		zap.String("scene_id", input.SceneID),
		zap.String("language", string(input.Language)),
		zap.String("filename", filename),
		zap.String("text_preview", truncateText(input.Text, 80)),
	)

	result, err := g.Exec.Execute(ctx, cmd)
	if err != nil {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: TTS generation for scene %s language %s failed: %w",
			input.SceneID, input.Language, err)
	}
	if result == nil {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: pipeline returned nil result for scene %s language %s",
			input.SceneID, input.Language)
	}
	if result.Status != voiceover.StatusCompleted {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: pipeline status %s for scene %s language %s: %s",
			result.Status, input.SceneID, input.Language, result.Error)
	}

	filePath := result.CleanedPath
	if filePath == "" {
		filePath = result.LocalPath
	}
	if filePath == "" {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: pipeline produced no local audio path for scene %s language %s",
			input.SceneID, input.Language)
	}

	ref := scriptgen.AudioReference{
		ID:       result.LegacyFileMD5,
		URL:      result.DriveLink,
		FilePath: filePath,
		Duration: float64(result.DurationMs) / 1000.0,
	}
	if ref.Duration <= 0 {
		return scriptgen.AudioReference{}, fmt.Errorf("voiceover scriptgen: pipeline returned non-positive duration for scene %s language %s", input.SceneID, input.Language)
	}

	// Carry the full canonical artifact (word-level SSOT) so the runner's
	// phrase→timestamp projection is derived from the same bytes as the
	// audio. Nil when timing was disabled/unavailable/failed.
	if result.Timing != nil && result.Timing.Artifact != nil {
		ref.Timing = result.Timing.Artifact
	}
	// Carry the published timing bundle summary (timing.json SSOT + optional
	// SRT/VTT links + hashes) so the document renderer can surface the
	// original timing links. The word-level array stays in Timing.
	if result.Timing != nil {
		ref.TimingBundle = &scriptpkg.VoiceoverTimingBinding{
			Status:       string(result.Timing.Status),
			JSONLink:     result.Timing.JSONLink,
			SRTLink:      result.Timing.SRTLink,
			VTTLink:      result.Timing.VTTLink,
			BoundaryMode: result.Timing.BoundaryMode,
			WordCount:    result.Timing.WordCount,
			DurationUS:   result.Timing.DurationUS,
			TextSHA256:   result.Timing.TextSHA256,
			AudioSHA256:  result.Timing.AudioSHA256,
		}
	}

	g.Log.Info("voiceover scriptgen: TTS generated",
		zap.String("scene_id", input.SceneID),
		zap.String("language", string(input.Language)),
		zap.String("file_path", filePath),
		zap.String("file_hash", result.LegacyFileMD5),
		zap.Bool("timing_captured", ref.Timing != nil),
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
