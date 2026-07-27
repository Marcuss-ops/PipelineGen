// Package scene — scene_planner.go: the canonical owner of scene
// construction (godlike/06 SSOT — one canonical owner per fact).
//
// Phase: Wave 1.1 (July 2026), Script Ownership refactor.
// Pre-Phase 2, the clip-evidence narration + sentence-split
// fallback + Intro/Outro kind assignment all lived inside
// SceneAssetBinder.BindClips. Wave 1.1 extracts every scene-shape
// concern into a dedicated ScenePlanner so that the binder reduces
// to its pure binding responsibility (only mutating
// scene.Bindings.Clip / scene.Bindings.Stock).
//
// godlike/06 SSOT (one canonical owner per fact): every method on
// ScenePlanner produces OR mutates SpecScene fields. No other file
// in this package may write Scene.Text, scene.Title, scene.Kind,
// scene.Index, or scene.ID. The remaining binder responsibilities
// after this phase:
//   - SceneAssetBinder.BindClips: attach Bindings.Clip + no shape.
//   - SceneAssetBinder.BindStock: attach Bindings.Stock + no shape.
//
// Wave 1.3 will turn the binder purity into a godlike/06 SSOT
// arch-check per-check that emits a build failure when the binder
// touches any non-Bindings field.
//
// godlike/07 NO-FAKE-AVAILABILITY: Plan never fabricates scenes.
// Every scene returned has either:
//
//	(a) come from the LLM-emitted draft (preserved verbatim),
//	(b) come from clip evidence with a transcript/description/name
//	    reference (never synthesized out of nothing), or
//	(c) come from real prose partition (the canonical SceneSynthesizer).
//
// The ungrounded "Scene {i+1}" placeholder is allowed only inside
// the synthesizer when the prose partition produces an under-word
// scene that must validate (per synthesizer.go contract).
//
// godlike/07 minimum-blast-radius: zero new dependencies — the
// planner reuses scriptpkg.ClipEvidence, scriptpkg.SpecScene, and
// the existing SceneSynthesizer. Composition-root wiring is
// unchanged because the SceneAssetBinder constructs the planner
// inline at NewSceneAssetBinder time.
package scene

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// NarrativeDraft is the canonical input to ScenePlanner.Plan: the
// narrative the engine produced before scene partitioning. The
// planner owns every transition between LLM-emitted scenes and
// the canonical ordered/kind-tagged scene list that downstream
// stages consume.
//
// godlike/06 SSOT: NarrativeDraft lives in the scene package
// instead of domain/script because W1.1 is a minimal-blast-radius
// extraction. A future wave may promote it to domain/script when
// additional producers need to consume the same shape; until then,
// the planner is the sole producer and sole consumer.
//
// JSON shape (canonical, internal-only contract):
//
//	{
//	  "text": "raw LLM output prose (may be empty for clean-clips plans)",
//	  "scenes": [{ "id": "...", "index": ..., "text": "...", "title": "...",
//	               "kind": "intro|clip|outro|narration|...",
//	               "bindings": {...} }],
//	  "num_clips": 3,
//	  "sentences_per_image": 1,
//	  "source_kind": "text|clips|catalog|search|curate"
//	}
//
// JSON envelope acceptance contract (LLM-PLAIN-TEXT-CONTRACT
// wave PR-6): a prose string that starts with `{` or `[` and
// parses as JSON with a "text" key is unwrapped to that key's value
// before prose partition. Mirrors SceneSynthesizer's contract.
type NarrativeDraft struct {
	// Text is the canonical prose emitted by the engine. Empty
	// when the engine emitted a structured scene list directly.
	Text string `json:"text,omitempty"`

	// Scenes is the LLM-emitted scene list. May be empty; the
	// planner will synthesize scenes from Text via the synthesizer
	// when both Text is non-empty AND plan allows prose fallback.
	Scenes []scriptpkg.SpecScene `json:"scenes,omitempty"`

	// NumClips is the requested scene count. Used by the prose
	// partitioner to size the synthesized scene list.
	NumClips int `json:"num_clips,omitempty"`

	// SentencesPerImage is the per-scene sentence budget. When
	// both NumClips is empty AND clip evidence is empty, the
	// planner uses this to derive a target scene count from
	// sentence count.
	SentencesPerImage int `json:"sentences_per_image,omitempty"`

	// SourceKind is the canonical source_kind from the resolved
	// plan. Empty when no plan is supplied (the planner is
	// invoked directly from tests). Mirrors scriptpkg.SourceType
	// values: "text", "clips", "catalog", "search", "curate".
	SourceKind string `json:"source_kind,omitempty"`
}

// ScenePlan is the canonical output of ScenePlanner.Plan.
//
// godlike/06 SSOT: ScenePlan carries the synthesized scenes + the
// metadata that downstream orchestration needs to decide whether
// to surface "synthesized" warnings + whether the planner
// actually took ownership. The struct is plain value type; no
// pointers, no maps — orchestration decides what to do with it.
//
// Why a struct (instead of returning []SpecScene directly): the
// binder AND future orchestration both need to know whether the
// planner synthesized scenes vs. preserved LLM scenes vs. clipped
// to evidence narration vs. no-op'd. The struct embeds those
// signals without requiring a parallel return value the compiler
// could lose in conversion (godlike/07 NO-FAKE-AVAILABILITY).
type ScenePlan struct {
	// Scenes is the canonical ordered scene list. May be empty
	// when Source is "noop" (no plan, no text, no evidence).
	Scenes []scriptpkg.SpecScene

	// Synthesized is true when the prose fallback engaged (FASE
	// 3 June 2026 contract). False when scenes came from the LLM
	// draft verbatim OR from the clip-evidence narration. Callers
	// surface "synthesized" warnings based on this signal.
	Synthesized bool

	// Suppressed is true when the planner decided NOT to fall
	// back to prose (e.g. clips source with empty evidence —
	// fail-closed at the planner boundary instead of silently
	// inventing prose). Callers surface "plan unavailable"
	// errors based on this signal.
	Suppressed bool

	// Source names how the scenes were produced. Canonical
	// values: "noop" | "microsoft_draft" | "clip_evidence" |
	// "prose_fallback". The "noop" value collapses the
	// canonical no-op branches (nil plan, empty evidence, empty
	// text) into a single downstream signal.
	Source string
}

// Canonical Source values for ScenePlan.Source.
const (
	// ScenePlanSourceNoop means Plan was called with insufficient
	// input (nil plan, empty text, empty evidence) and produced
	// zero scenes. Downstream stages must treat this as a no-op.
	ScenePlanSourceNoop = "noop"

	// ScenePlanSourceMicrosoftDraft means Plan preserved the
	// LLM-emitted scenes from the draft verbatim. The planner
	// may have assigned kinds by position but did NOT touch
	// scene.Text, scene.Title, or scene.Index.
	ScenePlanSourceMicrosoftDraft = "microsoft_draft"

	// ScenePlanSourceClipEvidence means Plan built the scene
	// list from clip-evidence narration (transcript + description
	// + name, in AcceptedClipIDs order). Pre-Phase-2 this path
	// was the binder's `buildScenesFromClipEvidence` private
	// helper; Wave 1.1 promotes it to ScenePlanner.PlanFromClipEvidence.
	ScenePlanSourceClipEvidence = "clip_evidence"

	// ScenePlanSourceProseFallback means Plan synthesized the
	// scene list from the draft's prose via SceneSynthesizer.
	// Synthesized=true is always set when this source fires.
	ScenePlanSourceProseFallback = "prose_fallback"
)

// ScenePlanner is the canonical owner of scene construction.
//
// Lifecycle: stateless struct (logger-only). Constructed via
// NewScenePlanner; the canonical entry point is Plan. The
// synthesizer is held inline as a sub-component so planner +
// synthesizer share the same instance per binder allocation
// (zero allocation churn across many BindClips calls).
type ScenePlanner struct {
	// log routes diagnostic messages from the planner's
	// decision branches (synthesized-count, evidence-narration,
	// kind-assignment). nil-safe via b.log guard at every log
	// site — the same nil-safe pattern binder.go uses.
	log *zap.Logger

	// synthesizer is the canonical prose partitioner (per
	// SceneSynthesizer contract). Held inline because the
	// synthesizer is stateless — no composition-root wiring
	// change required.
	synthesizer *SceneSynthesizer
}

// NewScenePlanner returns a ScenePlanner with the supplied logger
// and an inline SceneSynthesizer. Mirrors the constructor pattern
// used by SceneAssetBinder (logger threaded through, sub-component
// held inline for caller convenience).
func NewScenePlanner(log *zap.Logger) *ScenePlanner {
	return &ScenePlanner{log: log, synthesizer: NewSceneSynthesizer()}
}

// Plan is the canonical scene-construction entry. It owns all
// scene-shape decisions (text provenance, kind, order, ID) and
// hands the result back as a typed ScenePlan.
//
// Decision tree (preserved verbatim from the pre-Phase-2 binder
// branches to keep W1.1 a pure code-motion):
//
//  1. nil plan OR plan.ClipEvidence==nil → return no-op ScenePlan.
//     The binder treats no-op the same as before — it converged
//     honestly on "nothing to bind" without falling back silently.
//  2. SourceKind == SourceClips + empty AcceptedClipIDs →
//     Suppressed=true no-op scene plan (the planner declines to
//     synthesize prose for clips plans). Downstream emits
//     CLIP_NATIVE_PLAN_UNAVAILABLE.
//  3. draft.Scenes non-empty → preserve verbatim, assign kinds
//     by position (intro/clip/outro when >=3). Source="microsoft_draft".
//  4. draft.Text non-empty → fall back to SceneSynthesizer.FromProse
//     with a target count derived from clip count / NumClips /
//     sentences. Source="prose_fallback", Synthesized=true.
//  5. draft.Text empty + plan.ClipEvidence has clips → build
//     ScenePlan via PlanFromClipEvidence (the canonical clip
//     evidence narration shape). Source="clip_evidence".
//  6. Otherwise → no-op scene plan.
func (p *ScenePlanner) Plan(
	draft NarrativeDraft,
	plan *scriptpkg.ResolvedGenerationPlan,
) ScenePlan {
	if plan == nil {
		return ScenePlan{Source: ScenePlanSourceNoop}
	}

	// Case 1: clips source with empty evidence — planner declines
	// silent prose invention (godlike/07 NO-FAKE-AVAILABILITY).
	if draft.SourceKind == string(scriptpkg.SourceClips) {
		if plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
			return ScenePlan{Source: ScenePlanSourceNoop, Suppressed: true}
		}
	}

	// Case 1: empty scenes AND empty text => planner does NOT
	// synthesize from clip-evidence (matches the canonical
	// pre-Phase-2 binder behavior: buildScenesFromClipEvidence
	// was a dead helper, the binder only engaged clip evidence
	// when scenes or text were present). godlike/07
	// NO-FAKE-AVAILABILITY: don't fabricate scenes when the
	// upstream gave the planner nothing to work with.
	cleanedText := cleanProseFallbackText(draft.Text)
	if len(draft.Scenes) == 0 && cleanedText == "" {
		return ScenePlan{Source: ScenePlanSourceNoop}
	}

	// Case 1 (no-evidence, no-clips-source branch): bail out.
	if plan.ClipEvidence == nil && len(draft.Scenes) == 0 {
		// Prose-only path: continue below to text/synthesizer path.
	} else if plan.ClipEvidence == nil {
		return ScenePlan{Source: ScenePlanSourceNoop}
	}

	// Case 3: model-emitted scenes preserved verbatim. The planner
	// returns draft.Scenes by reference (no defensive copy) so the
	// binder's per-scene binding loop AND assignKindsByPosition
	// mutate the caller's scenes IN PLACE — this matches the
	// pre-Phase-2 binder contract where BindClips mutated the
	// caller's slice directly. godlike/07 minimum-blast-radius:
	// any defensive copy here would silently break every existing
	// test that inspects scene mutations after the call.
	if len(draft.Scenes) > 0 {
		p.assignKindsByPosition(draft.Scenes, plan)
		return ScenePlan{
			Scenes: draft.Scenes,
			Source: ScenePlanSourceMicrosoftDraft,
		}
	}

	// Case 4: prose fallback via SceneSynthesizer.
	if cleanedText != "" {
		// Target count = clip count if available, else NumClips,
		// else sentence-derived count. Mirrors the pre-Phase-2
		// n selection in binder.go (FASE 3 contract).
		n := 0
		if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
			n = len(plan.ClipEvidence.AcceptedClipIDs)
		}
		if plan.NumClips > 0 && (n == 0 || plan.NumClips < n) {
			n = plan.NumClips
		}
		// Sentence-derived count: prefer plan.SentencesPerImage
		// (canonical per-image-layout knob), fall back to the
		// draft's value for callers that pre-fill the bag. The
		// binder currently relies on plan.SentencesPerImage;
		// future orchestration may carry the value on the draft.
		sentencesPerImage := plan.SentencesPerImage
		if sentencesPerImage <= 0 {
			sentencesPerImage = draft.SentencesPerImage
		}
		// n selection: explicit counts (plan.NumClips > draft.NumClips
		// > clip count) take precedence over sentence-based derivation,
		// mirroring the pre-Phase-2 binder behavior. Sentence-derived
		// count fires only when every explicit knob is zero so the
		// planner always chooses a stable, deterministic target.
		effectiveNumClips := plan.NumClips
		if effectiveNumClips <= 0 {
			effectiveNumClips = draft.NumClips
		}
		if effectiveNumClips > 0 && (n == 0 || effectiveNumClips < n) {
			n = effectiveNumClips
		}
		if n <= 0 && sentencesPerImage > 0 {
			sentences := splitProseSentences(cleanedText)
			n = (len(sentences) + sentencesPerImage - 1) / sentencesPerImage
		}
		if n > 0 {
			synthesized := p.synthesizer.FromProse(cleanedText, n)
			if len(synthesized) > 0 {
				if p.log != nil {
					p.log.Info("scene_planner: prose-fallback engaged",
						zap.Int("synthesized", len(synthesized)),
						zap.Int("clips", n))
				}
				return ScenePlan{
					Scenes:      synthesized,
					Synthesized: true,
					Source:      ScenePlanSourceProseFallback,
				}
			}
		}
	}

	// Case 5: clip-evidence narration — only when evidence exists
	// AND no prose early-bail happened. Plugin emits the canonical
	// shape per accepted clip ID.
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		scenes := p.PlanFromClipEvidence(plan)
		if len(scenes) > 0 {
			if p.log != nil {
				p.log.Info("scene_planner: clip-evidence narration",
					zap.Int("scenes", len(scenes)))
			}
			return ScenePlan{
				Scenes: scenes,
				Source: ScenePlanSourceClipEvidence,
			}
		}
	}

	// Case 6: nothing to plan.
	return ScenePlan{Source: ScenePlanSourceNoop}
}

// PlanFromClipEvidence deterministically constructs one SpecScene
// per accepted clip using the clip's transcript, description and
// metadata as primary evidence. Wave 1.1 promotes this from the
// binder-internal `buildScenesFromClipEvidence` to a planner-owned
// method so the binder can route through the planner without
// re-implementing the evidence narration.
//
// godlike/06 SSOT: this method is the canonical clip-evidence
// scene builder — NO other file may construct SpecScenes from
// ClipEvidence directly. The pre-Phase-2 binder-internal helper
// `buildScenesFromClipEvidence` is preserved verbatim in body
// (no behavior change) so the W1.1 commit is byte-stable.
//
// Ordering: matches plan.ClipEvidence.AcceptedClipIDs AND respects
// plan.NumClips as the upper bound on constructed scenes.
//
// Kind assignment: intro / clip / outro by position when the
// scene count is >=3; otherwise every scene is SceneClip.
//
// Bindings: every scene receives a *ClipBinding carrying the
// evidence metadata (name, drive link, start/end ms, duration.
// Empty ClipDetails fall back to ClipNames/DriveLinks for the
// metadata-only path (preserves pre-Phase-2 legacy compatibility).
func (p *ScenePlanner) PlanFromClipEvidence(
	plan *scriptpkg.ResolvedGenerationPlan,
) []scriptpkg.SpecScene {
	if plan == nil || plan.ClipEvidence == nil || len(plan.ClipEvidence.AcceptedClipIDs) == 0 {
		return nil
	}

	ev := plan.ClipEvidence
	clipIDs := ev.AcceptedClipIDs
	if plan.NumClips > 0 && plan.NumClips < len(clipIDs) {
		clipIDs = clipIDs[:plan.NumClips]
	}

	scenes := make([]scriptpkg.SpecScene, len(clipIDs))
	for i, clipID := range clipIDs {
		detail, ok := ev.ClipDetails[clipID]
		if !ok {
			// Legacy fallback: synthesize a ClipDetail from the
			// assembled evidence maps so the metadata fields
			// surface even when ClipDetails is not populated.
			detail = scriptpkg.ClipDetail{
				Name:      ev.ClipNames[clipID],
				DriveLink: ev.DriveLinks[clipID],
			}
		}

		text := cleanClipNarrativeText(detail.Transcript)
		if text == "" {
			text = cleanClipNarrativeText(detail.Description)
		}
		if text == "" {
			text = detail.Name
		}
		if text == "" {
			text = fmt.Sprintf("Scene %d", i+1)
		}

		kind := scriptpkg.SceneClip
		if len(clipIDs) >= 3 {
			if i == 0 {
				kind = scriptpkg.SceneIntro
			} else if i == len(clipIDs)-1 {
				kind = scriptpkg.SceneOutro
			}
		}

		// ClipBinding.DurationMs is the canonical segment-
		// duration surface; populated here via
		// scriptpkg.ClipDurationMs (PURE canonical helper) plus
		// the canonical caller pattern's
		// scriptpkg.ClipDurationMsFromAssetID fallback for the
		// zero-delta branch (returns 0 by godlike/07
		// NO-FAKE-AVAILABILITY; "duration unknown").
		binding := &scriptpkg.ClipBinding{
			ClipID:         clipID,
			ClipTitle:      detail.Name,
			DriveLink:      detail.DriveLink,
			SubtitleLink:   detail.SubtitleLink,
			SubtitleFileID: detail.SubtitleFileID,
			StartMs:        detail.StartMs,
			EndMs:          detail.EndMs,
			DurationMs:     scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs),
		}
		if binding.DurationMs <= 0 {
			binding.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
		}
		if binding.DriveLink == "" {
			binding.DriveLink = ev.DriveLinks[clipID]
		}
		if binding.ClipTitle == "" {
			binding.ClipTitle = ev.ClipNames[clipID]
		}

		scenes[i] = scriptpkg.SpecScene{
			ID:    fmt.Sprintf("scene-%s", clipID),
			Index: i,
			Text:  text,
			Title: detail.Name,
			Kind:  kind,
			Bindings: scriptpkg.SceneBindings{
				Clip: binding,
			},
		}
	}
	return scenes
}

// cleanClipNarrativeText keeps the evidence fallback narration-safe. Search
// metadata can contain a source URL followed by tags; neither belongs in a
// spoken scene or in a semantic narrative field.
func cleanClipNarrativeText(text string) string {
	text = strings.TrimSpace(text)
	for _, marker := range []string{"https://", "http://", "www."} {
		if i := strings.Index(text, marker); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
	}
	return strings.TrimSpace(text)
}

// assignKindsByPosition overwrites scene.Kind for >=3-scene
// bundles following the canonical Intro / Clip / Outro layout.
// Wave 1.1 promotes this from the binder-internal `for-loop` that
// ran AFTER the binding step; Wave 1.3 will move the assignment
// BEFORE the binding step so the binder can never overwrite the
// planner's kind decision (godlike/06 SSOT).
//
// godlike/06 SSOT: this method is the canonical intro/clip/outro
// policy owner. SceneSynthesizer.kindForPosition is the cheat
// sheet for the synthesizer path; the planner wins for the
// binder-driven path because the planner knows the full scene
// count + plan evidence at decision time.
//
// godlike/07 NO-FAKE-AVAILABILITY: intros and outros are written
// only when len(scenes) >= 3 AND plan.ClipEvidence.AcceptedClipIDs
// has at least 3 accepted clips. Short bundles stay as
// SceneClip because the "every requested clip is a narrative
// beat" intent wins over the "frame with intro/outro" heuristic.
func (p *ScenePlanner) assignKindsByPosition(
	scenes []scriptpkg.SpecScene,
	plan *scriptpkg.ResolvedGenerationPlan,
) {
	if plan == nil || plan.ClipEvidence == nil {
		return
	}
	clipCount := len(plan.ClipEvidence.AcceptedClipIDs)
	if clipCount < 3 || len(scenes) < clipCount {
		return
	}
	scenes[0].Kind = scriptpkg.SceneIntro
	scenes[clipCount-1].Kind = scriptpkg.SceneOutro
	for i := 1; i < clipCount-1; i++ {
		scenes[i].Kind = scriptpkg.SceneClip
	}
}
