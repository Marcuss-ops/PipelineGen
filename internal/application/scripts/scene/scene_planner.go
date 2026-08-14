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
//
// File layout (split by concern, July 2026):
//
//	scene_planner.go          core: types, constants, struct, constructor, Plan orchestrator
//	scene_planner_evidence.go clip-evidence narration: PlanFromClipEvidence + cleanClipNarrativeText
//	scene_planner_kinds.go    intro/clip/outro policy: assignKindsByPosition
package scene

import (
	"math"
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

// Synthesizer returns the canonical stateless prose partitioner for callers
// that must preserve generated narrative while materializing clip slots.
func (p *ScenePlanner) Synthesizer() *SceneSynthesizer {
	if p == nil || p.synthesizer == nil {
		return NewSceneSynthesizer()
	}
	return p.synthesizer
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
	}

	// Clip-primary sources share one structural contract: accepted clips
	// become one canonical scene each. This also applies to source.search
	// after Qdrant selection; the renderer must not receive one opaque prose
	// scene when the resolver has accepted multiple clip bindings.
	if len(plan.Segments) == 0 && requiresClipNativePlan(plan) && len(plan.ClipEvidence.AcceptedClipIDs) > 0 &&
		len(draft.Scenes) != len(plan.ClipEvidence.AcceptedClipIDs) {
		scenes := p.PlanFromClipEvidence(plan)
		if len(scenes) > 0 {
			return ScenePlan{Scenes: scenes, Source: ScenePlanSourceClipEvidence}
		}
	}

	// Case 3: model-emitted scenes preserved verbatim. A single model
	// scene is the common plain-text response from small models; when
	// the request declares a per-segment word budget, it is only a
	// provisional envelope and must be materialized into ordered
	// narrative segments before downstream processors run.
	if len(draft.Scenes) == 1 && plan.SegmentWords > 0 {
		text := strings.TrimSpace(draft.Scenes[0].Text)
		if text == "" {
			text = cleanedText
		}
		wordCount := len(strings.Fields(text))
		n := len(splitProseParagraphs(text))
		if len(plan.Segments) > 0 {
			// Explicit segments are the authoritative scene cardinality;
			// clip count and prose paragraph heuristics must not split or
			// merge the caller's editorial slots.
			n = len(plan.Segments)
		} else if n < 2 && plan.SegmentWords > 0 && wordCount > plan.SegmentWords {
			n = int(math.Ceil(float64(wordCount) / float64(plan.SegmentWords)))
		}
		if n >= 2 {
			synthesized := p.synthesizer.FromProse(text, n)
			if len(synthesized) > 0 {
				if p.log != nil {
					p.log.Info("scene_planner: materialized single scene",
						zap.Int("materialized", len(synthesized)),
						zap.Int("paragraphs", len(splitProseParagraphs(text))),
						zap.Int("segment_words", plan.SegmentWords))
				}
				return ScenePlan{
					Scenes:      synthesized,
					Synthesized: true,
					Source:      ScenePlanSourceProseFallback,
				}
			}
		}
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
		if len(plan.Segments) > 0 {
			// N declared segments always produce N scenes, including
			// text-only segments and segments containing multiple clips.
			n = len(plan.Segments)
		} else if effectiveNumClips > 0 && (n == 0 || effectiveNumClips < n) {
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

func requiresClipNativePlan(plan *scriptpkg.ResolvedGenerationPlan) bool {
	if plan == nil || plan.ClipEvidence == nil {
		return false
	}
	return plan.SourceKind == string(scriptpkg.SourceClips) ||
		plan.GroundingPolicy == scriptpkg.GroundingPolicyClipsPrimary
}

// RequiresClipNativePlan exposes the shared source policy to postprocessors.
// Search results with clips_primary follow the same one-clip/one-scene
// contract as explicit clip sources.
func RequiresClipNativePlan(plan *scriptpkg.ResolvedGenerationPlan) bool {
	return requiresClipNativePlan(plan)
}
