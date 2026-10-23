// Package scriptgeneration — overlay_plan.go owns the derivation of the
// semantic OverlayPlan from a COMPLETED REAL result. It is the production
// counterpart of the fixture-driven planner/resolver tests: it feeds the
// certified timing surfaces (phrase timings, entity timeline, word timing)
// of an actual run into the overlay planner and resolver, so every candidate
// carries real timestamps — never estimates. The final production budget
// keeps the certified run-level ceiling of grounded phrases (overridable per
// request through max_phrase_overlays) and twenty materialized images per run.
//
// Ownership split (single owner per surface):
//
//	Phrase and image candidates → overlays.BuildPlan, from grounded scene
//	annotations and certified word timing.
//	Entity-bound image candidates → entities.ResolveEntityOverlayPlan, from
//	the certified EntityTimeline.
//	Other candidate types are discarded by the final editorial image+phrase budget.
//
// Every template terminates in one of the four canonical primitives
// (Text / Image / Video / Shape). The returned plan is the SEMANTIC
// renderinggen.overlay-plan.v1 document; RenderingGen lowers it to
// chronon.render-plan.v2 — PipelineGen never emits a concrete Chronon plan.
package scriptgeneration

import (
	"context"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// CompileOverlayPlan derives the full semantic OverlayPlan for a completed
// real result. It returns nil (no-op) when the result carries no derivable
// overlay surface (no annotations, no word timing, no entity timeline) and
// an error when a scene that DID carry timing/annotations cannot be
// projected — fail-closed like the phrase and entity timeline projections.
//
// Timestamps come exclusively from certified surfaces:
//
//   - Phrase and keyword candidates are located against the scene's real word
//     timing (LocatePhraseTimings). A phrase the voiceover did not speak is
//     skipped — never timestamped. The run-level editorial contract admits
//     phrase overlays and images only.
//   - Image candidates use their certified occurrence windows and require a
//     materialized asset before they can enter the final plan.
//
// The returned plan is sealed (render keys + fingerprint) and ready to
// enqueue through QueueRenderEnqueuer.EnqueueChrononPlan.
func CompileOverlayPlan(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, nil, perSceneImages...)
}

// CompileOverlayPlanWithPlates is CompileOverlayPlan with the run's certified
// basemap plate resolver wired: grounded place annotations covered by a plate
// become map overlay items. A nil resolver behaves exactly like
// CompileOverlayPlan (no maps — the fail-closed default deployment).
func CompileOverlayPlanWithPlates(result *GenerateResult, language Language, canvas OverlayCanvasSpec, planID, videoID, projectID string, resolver capabilityoverlay.PlateResolver, perSceneImages ...bool) (*capabilityoverlay.OverlayPlan, error) {
	return compileOverlayPlanWithMotionOffset(result, language, canvas, planID, videoID, projectID, nil, resolver, perSceneImages...)
}

// ── Visual bullet values (merged from editorial_bullet_values.go) ──
//
// Merged here to keep the registered scripts hotspot at its committed
// baseline: bullets are editorial overlay derivation. No behaviour change.

// visualBulletCandidateLimit bounds the VisualNER sentences requested per
// scene. Bullets are one per entity-bearing sentence, so a small bound is
// enough and keeps the persistent worker's output bounded.
const visualBulletCandidateLimit = 12

// attachVisualSemantics is the single-analyze path. When the wired analyzer
// implements SceneSemanticBatchAnalyzer (the production semantic engine
// does), ONE call covers every scene of the script. Otherwise the same
// analyzer is called once per scene, which is still a single analysis per
// scene — never the retired separate bullet + chart passes.
//
// Chart projections are applied when the editorial gate is open. Bullet
// candidates remain in the semantic manifest for planning and audit, while
// the already selected Phrase Impact bullet stays authoritative for editorial
// ranking, repetition control and scene budget. A failed batch keeps every
// existing value; analysis never fails the generation.
func (r *Runner) attachVisualSemantics(
	ctx context.Context,
	language string,
	inputs []scriptpkg.SceneAnalysisInput,
	highlights []scriptpkg.SceneHighlight,
	chartsEnabled bool,
) []scriptpkg.SceneHighlight {
	if r == nil || r.sceneSemanticAnalyzer == nil || len(highlights) == 0 {
		return highlights
	}
	bundles, ok := r.analyzeSemanticBundles(ctx, language, inputs)
	if !ok {
		return highlights
	}
	return r.mergeSemanticCharts(inputs, highlights, bundles, chartsEnabled)
}

func semanticBulletSpans(transcript string, inputs []scriptpkg.SceneAnalysisInput, bundles map[string]*SemanticCandidateBundle) ([]scriptpkg.SemanticBulletSpan, bool) {
	if len(inputs) == 0 {
		return nil, false
	}
	for _, in := range inputs {
		if in.StartByte == nil || in.EndByte == nil || *in.StartByte < 0 || *in.EndByte <= *in.StartByte || *in.EndByte > len(transcript) || transcript[*in.StartByte:*in.EndByte] != in.Text {
			return nil, false
		}
	}
	spans := make([]scriptpkg.SemanticBulletSpan, 0)
	for _, in := range inputs {
		bundle := bundles[in.SceneID]
		if bundle == nil {
			continue
		}
		for _, candidate := range bundle.Bullets {
			if candidate.Start < 0 || candidate.End <= candidate.Start || candidate.End > len(in.Text) || in.Text[candidate.Start:candidate.End] != candidate.Text {
				continue
			}
			start, end := *in.StartByte+candidate.Start, *in.StartByte+candidate.End
			if end > len(transcript) || transcript[start:end] != candidate.Text {
				continue
			}
			score := candidate.Score
			spans = append(spans, scriptpkg.SemanticBulletSpan{StartByte: start, EndByte: end, Text: candidate.Text, Score: &score})
		}
	}
	return spans, true
}

// analyzeSemanticBundles reuses the production batch interface so the caller
// can pass the same extraction to Phrase Impact and visual projections.
func (r *Runner) analyzeSemanticBundles(ctx context.Context, language string, inputs []scriptpkg.SceneAnalysisInput) (map[string]*SemanticCandidateBundle, bool) {
	bundles := make(map[string]*SemanticCandidateBundle, len(inputs))
	if r == nil || r.sceneSemanticAnalyzer == nil {
		return bundles, false
	}
	if batch, ok := r.sceneSemanticAnalyzer.(SceneSemanticBatchAnalyzer); ok {
		request := make([]SemanticSceneInput, 0, len(inputs))
		for _, in := range inputs {
			if strings.TrimSpace(in.Text) == "" {
				continue
			}
			request = append(request, SemanticSceneInput{SceneID: in.SceneID, Text: in.Text})
		}
		if len(request) > 0 {
			result, err := batch.SemanticBundles(ctx, language, request)
			if err != nil {
				r.log.Warn("semantic engine batch analysis unavailable; keeping existing values", zap.Error(err))
				return bundles, false
			}
			bundles = result
		}
	} else {
		texts := make(map[string]string, len(inputs))
		for _, in := range inputs {
			texts[in.SceneID] = in.Text
		}
		for _, in := range inputs {
			text := texts[in.SceneID]
			if strings.TrimSpace(text) == "" {
				continue
			}
			bundle, err := r.sceneSemanticAnalyzer.SemanticCandidates(ctx, language, text, visualBulletCandidateLimit)
			if err != nil {
				r.log.Warn("visualner semantic analysis unavailable; keeping existing values",
					zap.String("scene_id", in.SceneID), zap.Error(err))
				continue
			}
			bundles[in.SceneID] = bundle
		}
	}
	return bundles, true
}

// mergeSemanticMicroSummaries lets the editorial planner prefer the source
// sentence that carries a fact-backed micro summary. The summary text is a
// paraphrase and never becomes display copy: only its Evidence span, which
// the Go boundary re-verified byte-for-byte against the scene, can be timed
// and rendered. A matching existing bullet is promoted; when Phrase Impact
// produced no bullet, the fact-backed evidence becomes the scene candidate.
func mergeSemanticMicroSummaries(
	inputs []scriptpkg.SceneAnalysisInput,
	highlights []scriptpkg.SceneHighlight,
	bundles map[string]*SemanticCandidateBundle,
) []scriptpkg.SceneHighlight {
	texts := make(map[string]string, len(inputs))
	for _, in := range inputs {
		texts[in.SceneID] = in.Text
	}
	for i := range highlights {
		text := texts[highlights[i].SceneID]
		bundle := bundles[highlights[i].SceneID]
		if bundle == nil || strings.TrimSpace(text) == "" || len(bundle.MicroSummaries) == 0 {
			continue
		}
		importance := make(map[string]float64, len(bundle.EntityFacts))
		for _, fact := range bundle.EntityFacts {
			importance[fact.ID] = fact.Importance
		}
		best := -1
		bestWeight := 0.0
		for index, summary := range bundle.MicroSummaries {
			evidence := strings.TrimSpace(summary.Evidence)
			if evidence == "" || !strings.Contains(text, evidence) || len(strings.Fields(evidence)) > 28 {
				continue
			}
			weight := summary.Score
			for _, factID := range summary.EvidenceFactIDs {
				if value := importance[factID]; value > weight {
					weight = value
				}
			}
			if best < 0 || weight > bestWeight {
				best, bestWeight = index, weight
			}
		}
		if best < 0 {
			continue
		}
		evidence := strings.TrimSpace(bundle.MicroSummaries[best].Evidence)
		promoted := -1
		for index, bullet := range highlights[i].Bullets {
			if strings.EqualFold(strings.TrimSpace(bullet.Text), evidence) {
				promoted = index
				break
			}
		}
		if promoted > 0 {
			promotedBullet := highlights[i].Bullets[promoted]
			copy(highlights[i].Bullets[1:promoted+1], highlights[i].Bullets[:promoted])
			highlights[i].Bullets[0] = promotedBullet
			continue
		}
		if promoted == 0 || len(highlights[i].Bullets) != 0 {
			continue
		}
		start, end, ok := editorialSentenceSpan(text, evidence)
		if !ok {
			continue
		}
		highlights[i].Bullets = append(highlights[i].Bullets, scriptpkg.SceneBulletSpan{
			SentenceStart: start, SentenceEnd: end, Text: evidence,
		})
	}
	return highlights
}

// editorialSentenceSpan returns the half-open sentence range whose trimmed
// text equals the grounded evidence string.
func editorialSentenceSpan(text, evidence string) (int, int, bool) {
	for index, sentence := range editorialSentences(text) {
		if strings.EqualFold(strings.TrimSpace(sentence), strings.TrimSpace(evidence)) {
			return index, index + 1, true
		}
	}
	return 0, 0, false
}

func (r *Runner) mergeSemanticCharts(
	inputs []scriptpkg.SceneAnalysisInput,
	highlights []scriptpkg.SceneHighlight,
	bundles map[string]*SemanticCandidateBundle,
	chartsEnabled bool,
) []scriptpkg.SceneHighlight {
	texts := make(map[string]string, len(inputs))
	for _, in := range inputs {
		texts[in.SceneID] = in.Text
	}
	for i := range highlights {
		bundle := bundles[highlights[i].SceneID]
		if bundle == nil {
			continue
		}
		text := texts[highlights[i].SceneID]
		if strings.TrimSpace(text) == "" {
			continue
		}
		if chartsEnabled {
			highlights[i].ChartValues = mergeChartValues(highlights[i].ChartValues, bundle.Charts, text)
		}
	}
	return highlights
}
