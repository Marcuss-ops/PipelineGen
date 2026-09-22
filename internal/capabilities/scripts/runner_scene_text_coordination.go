// Package scriptgeneration — runner_scene_text_coordination.go owns the scene
// TEXT coordination helpers of the run: the fixed-media display-text translation
// pass and the reason vocabulary for the streaming/batch scene-text path choice.
//
// Materialised as a cohesive sibling of runner_execution.go (Sept 2026) to keep
// that file under the architecture cap of 600 LOC without changing behaviour: the
// execution wrapper stays the owner of the phase decomposition, while the two
// helpers below — both about scene text, not about phase ordering — live beside
// it in the same package. Splitting in the SAME directory is the registered
// hotspot's documented trade (see architecture/package_hotspots.json: per-file
// size down, file count up, ceiling re-locked to the measured count).
package scriptgeneration

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// processFixedDisplayText translates fixed-media display text into every
// target language. Fixed media never enters TTS/narration, but its display
// text remains a subtitle surface for localized renders.
func (c *sceneReadyCoordinator) processFixedDisplayText(out Scene) (Scene, error) {
	if !out.ExecutionMode.AllowsDisplayTextTranslation() {
		return out, nil
	}
	if out.Text == nil {
		out.Text = make(map[Language]string)
	}
	sourceText := strings.TrimSpace(out.Text[c.req.SourceLanguage])
	if sourceText == "" {
		return out, nil
	}
	langs := make([]Language, 0, len(c.req.Languages))
	seen := map[Language]bool{}
	for _, lang := range c.req.Languages {
		if lang == "" || lang == c.req.SourceLanguage || seen[lang] {
			continue
		}
		seen[lang] = true
		if out.Text[lang] != "" {
			continue
		}
		langs = append(langs, lang)
	}
	work := make([]sceneLanguageWork, 0, len(langs))
	for _, lang := range langs {
		work = append(work, sceneLanguageWork{lang: lang, needsTranslation: true})
	}
	outcomes, err := concurrent.Map(c.ctx, work, c.translationSlots.Cap(), func(ctx context.Context, itemIdx int, item sceneLanguageWork) (sceneLanguageOutcome, error) {
		translated, err := c.translateLanguage(ctx, itemIdx, out.ID, item.lang, sourceText)
		if err != nil {
			return sceneLanguageOutcome{}, err
		}
		return sceneLanguageOutcome{lang: item.lang, text: translated, translated: true}, nil
	})
	if err != nil {
		return Scene{}, err
	}
	for _, res := range outcomes {
		if res.translated {
			out.Text[res.lang] = res.text
		}
	}
	for _, res := range outcomes {
		if !res.translated {
			continue
		}
		if err := c.runner.recordArtifactOperation(c.ctx, c.exec, ArtifactOperation{
			OperationID: artifactOperationID(c.exec.Attempt, OperationTranslation, out.ID, string(res.lang)),
			Kind:        OperationTranslation,
			SceneID:     out.ID,
			Language:    res.lang,
			Status:      "COMPLETED",
		}); err != nil {
			return Scene{}, err
		}
	}
	c.mu.Lock()
	c.transCalls += len(outcomes)
	c.mu.Unlock()
	return out, nil
}

// sceneTextPathReason names why a run took the streaming or batch scene-text
// path. The runner owns the decision; this keeps the observability reason
// independent from the coordinator implementation.
func sceneTextPathReason(req GenerateRequest, streamed, topologyNeedsMaterialization bool, gen TextGenerator) string {
	switch {
	case streamed:
		return "streamed"
	case req.ScriptParams.SourceTextVerbatim:
		return "batch_source_text_verbatim"
	case len(req.MediaPlan.Extraction.ImportantPhrases) > 0:
		return "batch_important_phrase_hints"
	case req.Intro != nil || req.Outro != nil:
		return "batch_intro_outro"
	case topologyNeedsMaterialization:
		return "batch_segment_topology"
	case req.Source.Type == SourceClips && !SceneStreamingEligibility(req):
		return "batch_source_clips_ineligible"
	}
	if _, ok := gen.(SceneTextStreamer); !ok {
		return "batch_generator_not_streamable"
	}
	return "batch_reason_unclassified"
}
