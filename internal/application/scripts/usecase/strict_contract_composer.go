// Package usecase — strict_contract_composer.go owns the
// godlike/06 SSOT composition site for the LLM-COMPACT-CONTRACT
// wave (PR-CS-1 follow-up, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - ModelScriptOutputV1 (internal/domain/script/model_output.go)
//     is the canonical APPLICATION-SHAPE consumed by postprocessors
//   - storage.
//   - ModelOutput (internal/domain/script/model_output_strict.go)
//     is the canonical MODEL-SHAPE emitted by the LLM.
//   - This file is the ONLY canonical compose site that lifts the
//     MODEL-SHAPE into the APPLICATION-SHAPE. Any other lift would
//     duplicate the compose rule and create drift.
//
// godlike/07 NO-FAKE-AVAILABILITY: deriveValidRefs refuses to
// produce a degenerate slot set (falls back to a single slot-1
// when the plan carries no segments AND no clips), and
// composeModelOutputToMSOV1 joins segment text deterministically
// with "\n\n" — the canonical separator other engines may rely on.
package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// DeriveValidRefsFromPlan computes the slot set
// {slot-1, slot-2, ..., slot-N} the engine expects from the
// model. The slot count is the max of:
//
//   - len(plan.Segments)        (per-block payload)
//
//   - len(plan.ClipEvidence.AcceptedClipIDs)  (clip-grounded set)
//
//     floored at 1 (plan.NumClips is a prompt-side soft hint only).
//
// godlike/06 SSOT: this function is the ONLY authoritative
// slot-set builder. The prompt emit (buildSegmentInstructions +
// buildNarrativeClipViews) and the strict validator
// (script.ParseModelOutputStrict) must agree on what the model
// can return. If this function changes, both downstream sites
// must change in lock-step — covered by the same package.
//
// godlike/07 NO-FAKE-AVAILABILITY: nil plan produces
// {"slot-1"} so the engine never returns an empty validator
// input. A nil/empty validRefs would cause fail-closed
// ErrModelOutputRefNotInPlan at parse time even for a
// well-formed model output.
func DeriveValidRefsFromPlan(plan *scriptpkg.ResolvedGenerationPlan) map[string]struct{} {
	refs := make(map[string]struct{})
	if plan == nil {
		refs["slot-1"] = struct{}{}
		return refs
	}
	n := len(plan.Segments)
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > n {
		n = len(plan.ClipEvidence.AcceptedClipIDs)
	}
	// plan.NumClips is intentionally NOT a hard validator gate
	// here: it is a prompt-side soft hint ("Use exactly N clip-
	// driven scenes" in buildClipGroundingInstructions) and the
	// model MAY emit slots beyond it as long as they stay within
	// this function's wider range (len(AcceptedClipIDs)). The
	// compose site (composeModelOutputToMSOV1) seeds bindings
	// 1:1 across seg index — any slot-N within the plan range
	// gets a consistent ClipID + drive link, so capping the
	// validRefs at NumClips would only create a false-negative
	// mismatch when the model legitimately emits one extra slot.
	if n <= 0 {
		n = 1
	}
	for i := 1; i <= n; i++ {
		refs[fmt.Sprintf("slot-%d", i)] = struct{}{}
	}
	return refs
}

// composeModelOutputToMSOV1 lifts a validated
// ModelOutput (post ParseModelOutputStrict) into the
// canonical application-shape ModelScriptOutputV1.
//
// Composition rules:
//   - SchemaVersion = 1 (the canonical current version).
//   - SpecScene[].ID    = seg.Ref ("slot-N")
//   - SpecScene[].Index = i (zero-based, in emit order)
//   - SpecScene[].Text  = seg.Text verbatim
//   - SpecScene[].Kind  = SceneClip if a clip is bound
//     to that slot (= i < clipSlotCount),
//     SceneNarration otherwise.
//   - SpecScene[].Bindings.Clip = ClipBinding{ClipID, DriveLink}
//     seeded from plan.ClipEvidence when the slot is
//     clip-backed (1:1 mapping seg index → AcceptedClipIDs[i]).
//     The downstream SceneAssetBinder.BindClips (when it
//     runs as a postprocessor) may OVERWRITE these with the
//     same payload; the seed is what guarantees the engine's
//     direct MSOV1 output is self-sufficient for callers
//     that bypass the binder (e.g. unit tests, the
//     clip-native planning enforcement, etc.).
//   - output.Text       = joined seg.Text values with "\n\n"
//     as the canonical separator.
//
// godlike/06 SSOT: this is the ONLY compose site. Single-source
// rule — no other site lifts ModelOutput to MSOV1.
//
// godlike/07 NO-FAKE-AVAILABILITY: nil mo produces a
// MSOV1 with empty SpecScene and empty Text — the engine
// downstream surfaces this as a typed ErrModelOutputEmptySegments
// upstream (so a nil mo is never an "OK" silent no-op).
// Empty plan produces un-bound scenes (no clip evidence means
// no seed bindings — the strict clip-native enforcement
// surfaces that as a typed ErrClipNativePlanningFailed).
func composeModelOutputToMSOV1(
	mo *scriptpkg.ModelOutput,
	plan *scriptpkg.ResolvedGenerationPlan,
) *scriptpkg.ModelScriptOutputV1 {
	out := &scriptpkg.ModelScriptOutputV1{
		SchemaVersion: 1,
		SpecScene: scriptpkg.SpecSceneOutput{
			Version: 1,
		},
	}
	if mo == nil {
		out.SpecScene.Scenes = []scriptpkg.SpecScene{}
		return out
	}

	// Capture clip-grounded slot count + per-clip drive link map
	// BEFORE the per-segment loop so each scene can be seeded
	// with its clip binding in 1:1 order (slot-N → clip-N,
	// capped at min(len(segments), len(clips), NumClips)).
	clipSlotCount := 0
	var clipIDs []string
	var driveLinks map[string]string
	if plan != nil && plan.ClipEvidence != nil {
		clipIDs = plan.ClipEvidence.AcceptedClipIDs
		driveLinks = plan.ClipEvidence.DriveLinks
		clipSlotCount = len(clipIDs)
		if plan.NumClips > 0 && plan.NumClips < clipSlotCount {
			clipSlotCount = plan.NumClips
		}
	}
	// Cap by segment count: scenes beyond the clip count get
	// no binding (matches SceneAssetBinder.BindClips P0 #2
	// invariant — surface LLM mismatches instead of cycling).
	if clipSlotCount > len(mo.Segments) {
		clipSlotCount = len(mo.Segments)
	}

	scenes := make([]scriptpkg.SpecScene, 0, len(mo.Segments))
	var text strings.Builder
	for i, seg := range mo.Segments {
		if i > 0 {
			text.WriteString("\n\n")
		}
		text.WriteString(seg.Text)
		kind := scriptpkg.SceneNarration
		scene := scriptpkg.SpecScene{
			ID:           seg.Ref,
			Index:        i,
			Text:         seg.Text,
			EvidenceRefs: []string{seg.Ref},
		}
		if i < clipSlotCount {
			kind = scriptpkg.SceneClip
			// Seed the basic clip binding so downstream
			// consumers (clip-native planning, the binder,
			// direct MSOV1 readers) have an authoritative
			// source of truth WITHOUT requiring a separate
			// BindClips postprocessor pass.
			clipID := clipIDs[i]
			binding := &scriptpkg.ClipBinding{
				ClipID: clipID,
			}
			if driveLinks != nil {
				if dl, ok := driveLinks[clipID]; ok {
					binding.DriveLink = dl
				}
			}
			scene.Bindings.Clip = binding
		}
		scene.Kind = kind
		scenes = append(scenes, scene)
	}
	out.Text = text.String()
	out.SpecScene.Scenes = scenes
	return out
}
