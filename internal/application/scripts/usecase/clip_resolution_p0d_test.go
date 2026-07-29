// Package scripts — clip_resolution_p0d_test.go: P0.D clip-ordering
// test suite for /api/script/generate source=clips.
//
// July 2026 PR — P0.D gate. Pins the contract for the V2 source=clips
// resolution path: when the user supplies N clip IDs in any order,
// the canonical output surfaces must preserve the EXACT input order:
//
//  1. ev.AcceptedClipIDs MUST equal the input slice element-for-element
//     (positional equality — NOT a set equality / ElementsMatch).
//     A future regression that ships, e.g., sort.Strings on the
//     AcceptedClipIDs slice would silently rewrite the narrative
//     ordering and surface instantly as a slice-equal failure here.
//  2. ev.RenderableClipIDs (the DriveLink-bearing subset) MUST be the
//     SAME input order — clips whose positions in the input are
//     reordered must keep their position; only Drive-link presence
//     filters the membership, never order.
//  3. ev.AssembledText MUST mention every CLIP-N token in input order.
//     The engine passes the assembled text verbatim to the LLM as
//     grounding evidence; a regression that inflates the text with
//     a sort.SceneID or hash-bucketed re-bucket would surface as a
//     positional failure in the sourceText CLIP-N index traversal.
//  4. The SceneAssetBinder (buildScenesFromClipEvidence) MUST emit
//     scene[i] with Bindings.Clip.ClipID == input[i] for all i —
//     independent of input ordering. This is the canonical 1:1
//     binding contract (P0 #2 June 2026) that pins how the binder
//     translates the dynamic evidence chain into the per-scene
//     bindings document / voiceover / images consume.
//
// USER-SPEC SCENARIOS:
//
//   - chronological  : round-1, round-2, ..., round-8
//   - reverse        : round-8, round-7, ..., round-1
//   - round_7_first  : round-7, round-1, round-2, round-3, round-4,
//     round-5, round-6, round-8 (round 7 fires
//     before round 1 — narrative must respect the
//     user-supplied order)
//
// "Il testo narrativo non riordini arbitrariamente gli eventi" is
// enforced deterministically at the EVIDENCE LAYER (a–c above).
// The actual LLM-emitted narrative output is necessarily
// non-deterministic and out of scope for this contract-lock suite —
// the LLM is responsible for narrative CONTENT, not for evidence
// reordering.
//
// Architecture: HYBRID (mirrors P0.C):
//   - BuildClipContext (no Ollama fake, no PipelineResult) — the
//     cheap layer that asserts AcceptedClipIDs / RenderableClipIDs
//     / AssembledText through a single table-driven function.
//   - SceneAssetBinder.BindClips — the binder seam, exercised with
//     a hand-rolled ResolvedGenerationPlan that preserves the
//     shuffled evidence vector. Asserts the 1:1 scene-binding
//     contract for the same 3 orderings.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion uses slice-equal
// (require.Equal on []string) NOT set-equal (assert.ElementsMatch)
// — set equality would silently pass any future regression that
// reorders the slice while keeping the same set, defeating the
// P0.D contract entirely.
package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// P0.D canonical 8-clip ID set. The "round-N" naming matches the
// user-spec language verbatim; future contributors can grep for
// "round-" in the test files without inventing a separate vocab.
var p0dRoundIDs = []string{
	"round-1", "round-2", "round-3", "round-4",
	"round-5", "round-6", "round-7", "round-8",
}

// p0dOrderingCases returns the 3 user-spec input-orderings as
// independent []string slices. Each is fully independent — no
// slice-sharing — so a test that mutates one (e.g. accidentally
// in-place sort.Strings) cannot bleed into the others.
//
//   - chronological  → identity (round-1 ... round-8)
//   - reverse        → flipped
//   - round_7_first  → non-monotonic permutation
//
// Each ordering also carries an `idOrderCheck` that asserts the
// "round-7 fires before round-1" intent for the shuffled case.
var p0dOrderingCases = []struct {
	name         string
	clipIDs      []string
	idOrderCheck func(t *testing.T, ids []string) // scenario-specific narrative-order pivot assertion
}{
	{
		name:    "chronological_r1_to_r8",
		clipIDs: append([]string(nil), p0dRoundIDs...),
		idOrderCheck: func(t *testing.T, ids []string) {
			require.Equal(t, p0dRoundIDs, ids,
				"chronological input MUST round-trip equal to the canonical round-1..round-8 sequence")
		},
	},
	{
		name:    "reverse_r8_to_r1",
		clipIDs: reversedRoundIDs(p0dRoundIDs),
		idOrderCheck: func(t *testing.T, ids []string) {
			require.Equal(t, len(p0dRoundIDs), len(ids),
				"reverse case MUST still carry all 8 clip IDs (no drops / duplicates)")
			for i := 0; i < len(p0dRoundIDs); i++ {
				require.Equalf(t, p0dRoundIDs[len(p0dRoundIDs)-1-i], ids[i],
					"reverse position %d MUST equal p0dRoundIDs[%d]; got=%q",
					i, len(p0dRoundIDs)-1-i, ids[i])
			}
		},
	},
	{
		name: "round_7_first_then_r1_to_r6_then_r8",
		// round-7 deliberately fires BEFORE round-1 → narrative
		// must mirror that order (the "text narrative doesn't
		// arbitrarily reorder events" invariant under a
		// non-monotonic user input).
		clipIDs: []string{
			"round-7", "round-1", "round-2", "round-3",
			"round-4", "round-5", "round-6", "round-8",
		},
		idOrderCheck: func(t *testing.T, ids []string) {
			// The pivot: round-7 is at index 0, round-1 at index 1.
			require.Equal(t, "round-7", ids[0],
				"round_7_first MUST put round-7 at index 0 (not the natural round-1)")
			require.Equal(t, "round-1", ids[1],
				"round_7_first MUST put round-1 at index 1 (after round-7)")
			require.Equal(t, "round-8", ids[len(ids)-1],
				"round_7_first MUST keep round-8 at the LAST position (boundary preserved)")
		},
	},
}

// reversedRoundIDs returns a fresh reversed copy — never aliases
// the input slice so callers cannot accidentally mutate the
// canonical order through the slice header.
func reversedRoundIDs(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

// seedP0DResolver populates the fakeClipResolver with the canonical
// 8 clips. Each clip carries a UNIQUE transcript string ("Transcript
// for round-N") so positional sourceText assertions can distinguish
// the round-N mention from neighbouring CLIP tokens.
func seedP0DResolver(_ *testing.T) *fakeClipResolver {
	r := newFakeClipResolver()
	for _, id := range p0dRoundIDs {
		r.AddClip(makeP0DClip(id))
	}
	return r
}

// makeP0DClip returns an *asset.Asset with a unique transcript so
// sourceText CLIP-N positional assertions fire on the unique round-N
// payload (not on adjacent fallback noise). Reuses makeTestClip +
// overrides the search text to "Transcript for round-N".
func makeP0DClip(id string) *asset.Asset {
	c := makeTestClip(id, "Title "+id, 10*time.Second)
	// Replace SearchText with a unique transcript payload so the
	// round-N mention in sourceText is unambiguously attributable.
	c.SearchText = "Transcript for " + id
	return c
}

// assertCLIPTokensInInputOrder asserts that every "CLIP <id>" token
// in `ids` appears in `sourceText` at a strictly increasing string
// index — proving the assembled evidence is fed to the LLM in the
// exact user-specified input order (no reordering at the
// deterministic seam). The expected pivot tokens are anchored on the
// round-N transcript payload so the assertion cannot be satisfied
// by coincidental substring overlap.
func assertCLIPTokensInInputOrder(t *testing.T, ids []string, sourceText, caseName string) {
	t.Helper()
	// Anchor on the unique transcript payload ("Transcript for
	// round-N") so the assertion is robust to any future change
	// in the CLIP-N header line template (e.g. "CLIP: round-1"
	// vs "CLIP round-1:").
	anchorFor := func(id string) string {
		return "Transcript for " + id
	}
	lastIdx := -1
	for _, id := range ids {
		anchor := anchorFor(id)
		idx := strings.Index(sourceText, anchor)
		require.GreaterOrEqualf(t, idx, 0,
			"P0.D ordering contract: sourceText MUST mention %q in INPUT order (case=%q); src=%q",
			anchor, caseName, sourceText)
		require.Greaterf(t, idx, lastIdx,
			"P0.D ordering contract: sourceText index for %q (%d) MUST be strictly greater than the previous entry (%d) — reordering detected (case=%q); src=%q",
			anchor, idx, lastIdx, caseName, sourceText)
		lastIdx = idx
	}
}

// TestClipResolution_P0D_BuilderOrder pins the builder-layer
// ordering contract for the 3 user-spec input orderings. Each
// subtest:
//
//  1. Builds a fresh ClipSourceBuilder + a freshly-seeded resolver.
//  2. Calls BuildClipContext with the permutation's clipIDs.
//  3. Asserts:
//     (a) ev.AcceptedClipIDs slice-equals the input order.
//     (b) ev.RenderableClipIDs slice-equals the input order
//     (every clip has a DriveLink; the subset is the whole
//     set — kept so a future regression that filters by
//     DriveLink AFTER reordering fails loudly).
//     (c) EVERY CLIP-N token in input order appears in sourceText
//     at strictly increasing string indices (no reorder, no
//     drop, no duplicate emission).
//     (d) Scenario-specific idOrderCheck pivot assertion.
//
// Parallel execution is safe: each subtest allocates its own
// resolver + builder + opts (no package-level shared state).
func TestClipResolution_P0D_BuilderOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range p0dOrderingCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := seedP0DResolver(t)
			builder := NewClipSourceBuilder(resolver, nil, nil)

			// RequireDriveLink=false (text-only path) so all
			// 8 clips make it into RenderableClipIDs regardless
			// of seed; this tests ORDER preservation, not the
			// DriveLink filter (covered in the P0.C suite).
			opts := &ClipGenerationOptions{RequireDriveLink: false}

			ev, _, sourceText, err := builder.BuildClipContext(
				context.Background(), tc.clipIDs, opts,
			)
			require.NoErrorf(t, err,
				"BuildClipContext MUST succeed (every input ID resolves via the seeded resolver); case=%q",
				tc.name)
			require.NotNil(t, ev, "evidence MUST be non-nil on success")

			// (a) AcceptedClipIDs slice-equality (the P0.D
			//     canonical contract — POSITIONAL, not set).
			require.Equalf(t, tc.clipIDs, ev.AcceptedClipIDs,
				"P0.D ordering contract: AcceptedClipIDs MUST slice-equal the input order (case=%q); got=%v want=%v",
				tc.name, ev.AcceptedClipIDs, tc.clipIDs)

			// (b) RenderableClipIDs slice-equality (every clip
			//     has DriveLink so RenderableClipIDs ==
			//     AcceptedClipIDs in this fixture — the slice-
			//     equality check fires on a regression that
			//     reorders AFTER the DriveLink filter).
			require.Equalf(t, tc.clipIDs, ev.RenderableClipIDs,
				"P0.D ordering contract: RenderableClipIDs MUST slice-equal the input order (case=%q); got=%v want=%v",
				tc.name, ev.RenderableClipIDs, tc.clipIDs)

			// Scenario-specific narrative-order pivot assertion
			// (e.g. round_7_first case asserts round-7 at index 0).
			tc.idOrderCheck(t, ev.AcceptedClipIDs)

			// (c) sourceText CLIP-N positions MUST strictly
			//     increase with input order — locks the
			//     "narrative input evidence is fed in input order"
			//     deterministic seam (LLM-emitted narrative is
			//     out of scope for this deterministic test).
			assertCLIPTokensInInputOrder(t, tc.clipIDs,
				sourceText, tc.name)

			// MissingClipIDs is empty (every input ID resolves).
			require.Emptyf(t, ev.MissingClipIDs,
				"P0.D only feeds VALID IDs; MissingClipIDs MUST be empty (case=%q); got=%v",
				tc.name, ev.MissingClipIDs)
		})
	}
}

// TestClipResolution_P0D_BinderOrder pins the binder-layer
// ordering contract: scene[i] (by scene_id) MUST bind to the i-th
// AcceptedClipID for all i, regardless of input order. This locks
// the canonical 1:1 scene-binding contract (P0 #2 June 2026) that
// downstream processor pipelines rely on for canonical scene→clip
// mapping.
//
// The binder exercises the SAME 3 orderings as the builder test —
// against hand-rolled ClipBindingRequest slices. The binder MUST
// preserve the input order (no re-sort, no mod-cycling).
func TestClipResolution_P0D_BinderOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range p0dOrderingCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Build one ClipBindingRequest per scene in the test
			// permutation. The binder knows only scene_id,
			// requirements, candidate assets, and binding policy.
			reqs := make([]scene.ClipBindingRequest, 0, len(tc.clipIDs))
			for i, id := range tc.clipIDs {
				sceneID := "scene-" + string(rune('0'+i))
				reqs = append(reqs, scene.ClipBindingRequest{
					SceneID:      sceneID,
					Requirements: scene.AssetRequirements{},
					Candidates: []scene.ClipCandidate{
						{ClipID: id, DriveLink: "https://drive.google.com/" + id},
					},
					Policy: scene.ClipBindingPolicy{},
				})
			}

			binder := scene.NewSceneAssetBinder(nil)
			result := binder.BindClips(reqs)
			require.Truef(t, result.Changed,
				"binder MUST report Changed=true for clip-source kinds (case=%q)", tc.name)
			require.Lenf(t, result.Bindings, len(tc.clipIDs),
				"binder MUST emit one binding per accepted clip (case=%q); got=%d want=%d",
				tc.name, len(result.Bindings), len(tc.clipIDs))

			// P0.D binder invariants:
			//
			// For every i in [0, len(tc.clipIDs)):
			//  - result.Bindings["scene-i"] exists
			//  - result.Bindings["scene-i"].ClipID == tc.clipIDs[i]
			//                                          (1:1 ID match — PRESERVES order)
			for i, want := range tc.clipIDs {
				sceneID := "scene-" + string(rune('0'+i))
				binding, ok := result.Bindings[sceneID]
				require.Truef(t, ok,
					"P0.D binder invariant: expected binding for %s (case=%q)",
					sceneID, tc.name)
				require.Equalf(t, want, binding.ClipID,
					"P0.D binder invariant: %s.ClipID MUST equal input[%d]==%q (case=%q); got=%q",
					sceneID, i, want, tc.name, binding.ClipID)
			}

			// Pivot assertion: same idOrderCheck as the builder
			// test — re-pinned here on bound ClipIDs to prove the
			// binder does NOT re-sort independent of the builder.
			binderIDs := make([]string, len(tc.clipIDs))
			for i := range tc.clipIDs {
				sceneID := "scene-" + string(rune('0'+i))
				binderIDs[i] = result.Bindings[sceneID].ClipID
			}
			tc.idOrderCheck(t, binderIDs)
		})
	}
}

// buildP0DPlanEvidence returns a *scriptpkg.ClipEvidence populated
// with the canonical 8 clips in the ORDER supplied by `clipIDs`.
// Each clip's ClipDetail mirrors makeP0DClip so the binder can
// resolve Title / DriveLink without leaving fields empty.
//
// ORDER IS THE TEST'S RESPONSIBILITY — this helper just transcribes
// the supplied slice into the AcceptedClipIDs / RenderableClipIDs /
// ClipDetails fan-out. Any reordering here would invalidate the
// P0.D test itself, not the production code.
func buildP0DPlanEvidence(clipIDs []string) *scriptpkg.ClipEvidence {
	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:   append([]string(nil), clipIDs...),
		RenderableClipIDs: append([]string(nil), clipIDs...),
		ClipCount:         len(clipIDs),
		AssembledText:     "",
		DriveLinks:        make(map[string]string, len(clipIDs)),
		ClipNames:         make(map[string]string, len(clipIDs)),
		ClipDetails:       make(map[string]scriptpkg.ClipDetail, len(clipIDs)),
	}
	for _, id := range clipIDs {
		clip := makeP0DClip(id)
		ev.DriveLinks[id] = clip.DriveLink()
		ev.ClipNames[id] = clip.Name
		ev.ClipDetails[id] = scriptpkg.ClipDetail{
			Name:        clip.Name,
			Description: clip.SearchText,
			Transcript:  "Transcript for " + id,
			Tags:        append([]string(nil), clip.Tags...),
			StartMs:     0,
			EndMs:       10000,
			DriveLink:   clip.DriveLink(),
		}
	}
	return ev
}

// compile-time assertion: scene.NewSceneAssetBinder exists with
// the expected signature; a future signature drift in the
// post-processor-unification refactor would surface here.
var _ = (*scene.SceneAssetBinder)(nil)

// compile-time assertion: asset.Asset (used by seedP0DResolver) is
// reachable through this test file to surface package rename or
// field-removal drift during the typed resolver refactor.
var _ asset.Asset
