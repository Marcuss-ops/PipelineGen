package scriptgeneration

import (
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrLocalizedRenderFailure classifies a failed localized/fixed-media render
// without pretending it was a TTS failure. The durable stage stays
// GENERATING_VOICEOVERS because the render fan-out is dispatched from that
// phase and resuming at a later stage would skip the fan-out entirely; the
// stable error code is what separates a render defect from a TTS defect in the
// ledger and in retry classification.
var ErrLocalizedRenderFailure = errors.New("localized render failed")

// voiceoverWork is one independent TTS work item: a (scene, language, text)
// triple whose final scene text is immutable. The TTS branch depends only on
// this text — never on entities/phrases/words — so it fans out from the
// SceneTextReady boundary in parallel with SceneAnalysis.
type voiceoverWork struct {
	scene   *contract.Scene
	sceneID string
	lang    contract.Language
	text    string
}

// voiceoverResult is the per-item outcome of one TTS synthesis call.
type voiceoverResult struct {
	audioRef contract.AudioReference
	metric   contract.TTSSSceneMetric
}

// sanitizeNarrationForTTS is the runner-side entry point of the shared
// Markdown-to-speech contract. Keeping the pure implementation in the kernel
// lets the TTS provider boundary apply the exact same normalization.
func sanitizeNarrationForTTS(text string) string {
	return kernelscript.SanitizeForSpeech(text)
}

func buildVoiceoverWorkForLanguages(scenes []contract.Scene, sourceLanguage contract.Language, targetLanguages, voiceoverLanguages []contract.Language) []voiceoverWork {
	work := make([]voiceoverWork, 0)
	for i := range scenes {
		scene := &scenes[i]
		if scene.NativeClip || !scene.ExecutionMode.AllowsTTS() || !scene.ExecutionMode.AllowsGeneratedAudio() {
			continue
		}
		for _, item := range buildVoiceoverLanguageWork(scene.Text, sourceLanguage, targetLanguages, voiceoverLanguages) {
			text := sanitizeNarrationForTTS(item.Text)
			if text == "" {
				continue
			}
			if existing, ok := scene.Voiceover[item.Lang]; ok && existing.ID != "" {
				continue
			}
			work = append(work, voiceoverWork{scene: scene, sceneID: scene.ID, lang: item.Lang, text: text})
		}
	}
	return work
}

func (r *Runner) auditVoiceoverDispatch(runID string, req contract.GenerateRequest, result *contract.GenerateResult, work []voiceoverWork) (requested, reused int) {
	for _, scene := range result.Scenes {
		if scene.NativeClip || !scene.ExecutionMode.AllowsTTS() || !scene.ExecutionMode.AllowsGeneratedAudio() {
			continue
		}
		for _, item := range buildVoiceoverLanguageWork(scene.Text, req.SourceLanguage, req.Languages, req.VoiceoverLanguages) {
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			requested++
			if ref, ok := scene.Voiceover[item.Lang]; ok && ref.ID != "" {
				reused++
			}
		}
	}
	result.AudioMetrics.VoiceoverRequested = requested
	result.AudioMetrics.VoiceoverReused = reused
	result.AudioMetrics.VoiceoverGenerated = len(work)
	r.log.Info("voiceover dispatch audit",
		zap.String("run_id", runID),
		zap.Int("requested", requested),
		zap.Int("scene_projection_reused", reused),
		zap.Int("generated", len(work)),
	)
	return requested, reused
}
