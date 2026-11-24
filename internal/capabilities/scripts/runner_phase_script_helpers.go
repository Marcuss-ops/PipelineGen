package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// buildSceneAnalysisInputs locates each body scene verbatim in the transcript
// so the Rust worker can verify slice identity. Offsets stay nil when the
// scene text is not found exactly once (repeated text => nil, local-only).
func buildSceneAnalysisInputs(transcript string, scenes []contract.Scene, req contract.GenerateRequest) []scriptpkg.SceneAnalysisInput {
	out := make([]scriptpkg.SceneAnalysisInput, 0, len(scenes))
	for _, scene := range scenes {
		if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[req.SourceLanguage])
		if text == "" {
			continue
		}
		topic := scene.ID
		for _, spec := range req.ScriptParams.Segments {
			if spec.ID != "" && spec.ID == scene.ID && strings.TrimSpace(spec.Topic) != "" {
				topic = spec.Topic
				break
			}
		}
		in := scriptpkg.SceneAnalysisInput{SceneID: scene.ID, Text: text}
		t := strings.TrimSpace(topic)
		if t != "" {
			in.Topic = &t
		}
		// Verbatim single-occurrence locate; repeated text => nil offsets.
		if first := strings.Index(transcript, text); first >= 0 && strings.Count(transcript, text) == 1 {
			s, e := first, first+len(text)
			if e <= len(transcript) {
				in.StartByte, in.EndByte = &s, &e
			}
		}
		out = append(out, in)
	}
	return out
}

// buildEditorialManifest seals the per-scene products into editorial.v1 with
// a full fingerprint over everything influencing the result. Uncertified
// worker output still produces a manifest with Certified=false, never a
// fabricated certified one.
func buildEditorialManifest(req contract.GenerateRequest, inputs []scriptpkg.SceneAnalysisInput, res scriptpkg.PhraseImpactResult) *scriptpkg.EditorialManifest {
	ids := make([]string, len(inputs))
	texts := make([]string, len(inputs))
	topics := make([]string, len(inputs))
	offsets := make([][2]int64, len(inputs))
	for i, in := range inputs {
		ids[i] = in.SceneID
		texts[i] = in.Text
		if in.Topic != nil {
			topics[i] = *in.Topic
		}
		if in.StartByte != nil && in.EndByte != nil {
			offsets[i] = [2]int64{int64(*in.StartByte), int64(*in.EndByte)}
		} else {
			offsets[i] = [2]int64{-1, -1}
		}
	}
	scenes := make([]scriptpkg.EditorialScene, 0, len(res.SceneHighlights))
	for _, h := range res.SceneHighlights {
		bullets := make([]scriptpkg.EditorialSceneBullet, len(h.Bullets))
		for i, b := range h.Bullets {
			bullets[i] = scriptpkg.EditorialSceneBullet(b)
		}
		hl := make([]scriptpkg.EditorialHighlight, len(h.Highlights))
		for i, s := range h.Highlights {
			hl[i] = scriptpkg.EditorialHighlight(s)
		}
		scenes = append(scenes, scriptpkg.EditorialScene{
			SceneID: h.SceneID, Title: h.Title, TitleStatus: h.TitleStatus,
			TitleSource: h.TitleSource, Bullets: bullets, Highlights: hl,
			ChartValues: append([]string(nil), h.ChartValues...), GloballyIndexed: h.GloballyIndexed,
		})
	}
	fpIn := scriptpkg.EditorialFingerprintInput{
		Transcript:       strings.TrimSpace(res.Summary) + "\n" + strings.Join(texts, "\n\n"),
		SceneIDs:         ids,
		SceneTexts:       texts,
		SceneOffsets:     offsets,
		SceneTopics:      topics,
		Language:         string(req.SourceLanguage),
		ProfileVersion:   "highlights.v1",
		RankingWeights:   map[string]float64{"semantic": 1.0, "lexical": 1.0, "scene": 0.2, "evidence": 1.0},
		TokenizerVersion: "rust-splitter.v1",
		EmbeddingModel:   "lexical",
		EmbeddingVersion: "v1",
		AlgorithmVersion: "phrase-impact.scene-highlights.v1",
		SceneProducts:    scenes,
	}
	fp, err := scriptpkg.ComputeEditorialFingerprint(fpIn)
	if err != nil {
		return nil
	}
	m := &scriptpkg.EditorialManifest{
		SchemaVersion: scriptpkg.EditorialManifestVersion, SourceLanguage: string(req.SourceLanguage),
		ProfileVersion: "highlights.v1", Scenes: scenes,
		Certified:   res.SceneHighlightsCertified,
		Fingerprint: fp,
	}
	if err := m.Validate(); err != nil {
		m.Certified = false
	}
	return m
}

// The narration word floor lives in the kernel (script.MinimumClipSceneWords)
// so the ingress, `velox preflight` and this runner can never disagree about
// it. Clip-backed narrator intros and no_voiceover cutaways are exempted by
// script.NarrationFloorForScene; the former constant was the drift that let a
// ten-word narration pass every static check and die here after scheduling.

// outputFromScenes builds the single durable BODY narration projection from
// the ordered scene list. The requested source language wins; a first
// available language is only a compatibility fallback for older test
// generators. Protected fixed-media scenes are deliberately excluded: their
// DisplayText/text belongs to the timeline/document surface, never to the
// generated BODY word budget or minimum-word gate.
func outputFromScenes(scenes []contract.Scene, language contract.Language) contract.GenerateOutput {
	parts := make([]string, 0, len(scenes))
	fallbackUsed := false
	for _, scene := range scenes {
		if !scene.ExecutionMode.CountsTowardBodyWordBudget() {
			continue
		}
		text := strings.TrimSpace(scene.Text[language])
		if text == "" {
			// Deterministic fallback: pick lexicographically smallest lang key
			// instead of random Go map iteration, so output is stable across runs.
			bestLang := ""
			for l, candidate := range scene.Text {
				if strings.TrimSpace(candidate) == "" {
					continue
				}
				if bestLang == "" || string(l) < bestLang {
					bestLang = string(l)
					text = strings.TrimSpace(candidate)
				}
			}
			if text != "" {
				fallbackUsed = true
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n\n")
	return contract.GenerateOutput{
		Text:                       text,
		WordCount:                  len(strings.Fields(text)),
		SourceLanguageFallbackUsed: fallbackUsed,
	}
}

// bindExplicitClipSceneText preserves the caller's one-clip/one-scene
// contract. When a clips source supplies explicit SCENE N lines, the model
// may embellish each line but it must not collapse all narration into scene 1
// or leave later clip scenes empty. The binding is intentionally limited to
// the explicit marker format so free-form source text keeps its existing
// generation behavior.
func bindExplicitClipSceneText(req contract.GenerateRequest, scenes []contract.Scene) {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 || len(scenes) != len(req.Source.ClipIDs) {
		return
	}
	lines := make([]string, 0, len(scenes))
	for _, line := range strings.Split(req.Source.SourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 || strings.TrimSpace(line[colon+1:]) == "" {
			return
		}
		lines = append(lines, strings.TrimSpace(line[colon+1:]))
	}
	if len(lines) != len(scenes) {
		return
	}
	for i := range scenes {
		if scenes[i].Text == nil {
			scenes[i].Text = make(map[contract.Language]string)
		}
		// The per-clip source line is evidence/instructions, not the final
		// narration. Preserve a non-empty model answer; only use the supplied
		// line as a recovery value when generation left the scene empty.
		if strings.TrimSpace(scenes[i].Text[req.SourceLanguage]) == "" {
			scenes[i].Text[req.SourceLanguage] = lines[i]
		}
	}
}

// hasExplicitSceneMarkers returns true when sourceText contains "SCENE N:"
// markers that bindExplicitClipSceneText would use for post-generation
// rebinding. Streaming must be disabled when markers are present because
// scene text emitted scene-by-scene could be overwritten by the bind step.
func hasExplicitSceneMarkers(sourceText string) bool {
	for _, line := range strings.Split(sourceText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 || !strings.EqualFold(line[:5], "scene") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon >= 0 && strings.TrimSpace(line[colon+1:]) != "" {
			return true
		}
	}
	return false
}

// SceneStreamingEligibility determines whether a SourceClips request can
// safely use per-scene streaming (SceneTextReady fired scene-by-scene as
// each scene's text becomes final) instead of the barrier batch path.
//
// Streaming is safe when NO post-generation mutation will change scene
// text — the bindExplicitClipSceneText step is the only mutation, and it
// fires ONLY when SCENE N: markers are present in the source text. When
// markers are absent, the source text is already the definitive text OR
// the LLM generates fresh text from clip evidence alone.
//
// Eligibility conditions:
//
//  1. Source is SourceClips with at least 1 ClipID
//  2. Source text has NO explicit "SCENE N:" markers
//  3. Optional extra safety: ScriptParams.Segments are present (1:1 stable
//     clip→scene mapping); when absent the generator will emit a scene per
//     clip anyway, but explicit segments make the contract explicit.
//
// The "SCENE N:" marker check is the canonical signal: if present,
// bindExplicitClipSceneText WILL fire and could overwrite already-emitted
// scene text, corrupting downstream consumers (NLP, TTS, render) that
// already started on stale text.
func SceneStreamingEligibility(req contract.GenerateRequest) bool {
	if req.Source.Type != SourceClips || len(req.Source.ClipIDs) == 0 {
		return false
	}
	// SCENE N: markers → bindExplicitClipSceneText will fire → NOT streamable.
	if hasExplicitSceneMarkers(req.Source.SourceText) {
		return false
	}
	// Explicit Segments with 1:1 clip mapping strengthen the safe streaming
	// contract but are not mandatory: without them the generator still emits
	// one scene per clip. The marker check alone is the safety gate.
	return true
}

// validateMinimumGeneratedOutput only rejects empty narration. Target_words
// and min_words remain generation guidance and never block a completed script.
func validateMinimumGeneratedOutput(req contract.GenerateRequest, output contract.GenerateOutput) error {
	actual := len(strings.Fields(strings.TrimSpace(output.Text)))
	if actual == 0 {
		return fmt.Errorf("%w: generated narration is empty", ErrEmptyGeneratedText)
	}
	return nil
}

func contaminatedClipNarration(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, marker := range []string{"clip description:", "write a new", "do not copy the description", "source text:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (r *Runner) runNormalizePhase(ctx context.Context, runID string, exec ExecutionContext, resumeIdx int) bool {
	// ── Stage 1: Normalize ──────────────────────────────────────
	normalizeStep, startErr := r.startExecutionStep(ctx, exec, "NORMALIZE", "script")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageNormalizing, startErr)
		return false
	}
	if stageSkipped(resumeIdx, StageNormalizing) {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageNormalizing)))
	} else {
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageNormalizing)))
	}
	if stageSkipped(resumeIdx, StageNormalizing) {
		if err := r.skipExecutionStep(ctx, exec, normalizeStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageNormalizing, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, normalizeStep); err != nil {
		if stepErr := r.failExecutionStep(ctx, exec, normalizeStep, err); stepErr != nil {
			r.log.Warn("failed to record execution step failure", zap.String("step", normalizeStep.Name), zap.Error(stepErr))
		}
		r.failRunWithRetry(ctx, runID, StageNormalizing, err)
		return false
	}

	return true
}

// validateClipSceneOutput contains the source-specific safety checks for
// clip-backed narration. It keeps runSceneTextPhase focused on generation and
// leaves failure persistence at the same execution-step boundary.
func (r *Runner) validateClipSceneOutput(ctx context.Context, runID string, req contract.GenerateRequest, exec ExecutionContext, scriptStep ExecutionStep, scenes []contract.Scene) bool {
	for i, scene := range scenes {
		// no_voiceover cutaways are fixed media by design: their only text
		// surface is the clip's own footage. They must never be checked against
		// the narration word floor, whether or not Chronon will style the clip.
		if scene.NativeClip || scene.ExecutionMode.IsFixedMedia() {
			continue
		}
		// Short authored intro lines intentionally use only a few words and
		// no_voiceover cutaways have no narration surface: the kernel contract
		// returns zero for both. Every other scene keeps the canonical floor.
		minimumWords := scriptpkg.NarrationFloorForScene(req.ScriptParams.Segments, scene.ID)
		if minimumWords == 0 {
			// Exempt from the long-form floor, not from having copy at all:
			// one word is enough for an intro, zero is still a placeholder.
			minimumWords = 1
		}
		text := strings.TrimSpace(scene.Text[req.SourceLanguage])
		words := scriptpkg.WordCount(text)
		lower := strings.ToLower(text)
		placeholder := text == "" || words < minimumWords || lower == fmt.Sprintf("scene %d", i+1) || lower == "the"
		if placeholder || contaminatedClipNarration(text) {
			code := "SCRIPT_SCENE_TEXT_INVALID"
			if contaminatedClipNarration(text) {
				code = "SCRIPT_SCENE_TEXT_CONTAMINATED"
			}
			cause := fmt.Errorf("%s: scene=%d words=%d minimum=%d placeholder=%t", code, i, words, minimumWords, lower == fmt.Sprintf("scene %d", i+1) || lower == "the")
			if stepErr := r.failExecutionStep(ctx, exec, scriptStep, cause); stepErr != nil {
				r.log.Warn("failed to record execution step failure", zap.String("step", scriptStep.Name), zap.Error(stepErr))
			}
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, cause)
			return false
		}
	}
	return true
}
