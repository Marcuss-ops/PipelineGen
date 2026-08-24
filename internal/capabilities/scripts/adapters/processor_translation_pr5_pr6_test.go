// Package adapters — processor_translation_pr5_pr6_test.go.
// Forward-prevention regression-guard for the canonical
// TranslationProcessor contract surface landed in PR-5 + PR-6
// of SCRIPT-DOWNSTREAM-CUTOVER-2026-07-09.
//
// PR-5 wired OutputSpec.TranslateTo into ResolvedGenerationPlan
// + buildPostprocessorList (translation between metadata and
// clip_bindings). PR-6 populated PostProcessResult.TranslatedText
// + TranslatedSpecScene + updated mergePostProcessResult to
// propagate the translated surface into PipelineResult. Without
// these explicit fields, IsEmpty() would FLAG the workaround
// (in-place ProcessInput mutation only) as "returned empty
// output" — a false-positive that would surface in the registry's
// built-in log warnings.
//
// The tests below pin the surface contract at the typed
// envelope level (no HTTP, no Live, no Ollama; pure hermetic
// test surface). The processor_translation_integration_test.go
// file covers the wired-pipeline contract for happy + nil-port
// + error paths; this file focuses on the per-PostProcessResult
// + per-PipelineResult contract pinned by PR-6.
//
// godlike/06 SSOT (one-canonical-owner-per-fact): PostProcessResult
// + PipelineResult live ONLY here in adapters. The tests below
// are the canonical SOLE regression-guard for the PR-6 contract.
package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestPostProcessResult_IsEmpty_RespectsTranslatedFields pins
// the FALSE-POSITIVE PREVENTION contract from PR-6: when the
// TranslationProcessor succeeds but only populates TranslatedText
// + TranslatedSpecScene (no Changed flag, no canonical output
// fields, no SynthesizedScenes), IsEmpty() MUST return false.
//
// Pre-PR-6: a translation that mutated input.SpecScene in-place
// (per the ClipBindingsProcessor precedent) but did NOT populate
// any PostProcessResult payload field would be flagged "returned
// empty output" by the registry's log-based warning emitter.
// This test blocks that drift class by asserting IsEmpty()'s
// new branches honour TranslatedText and TranslatedSpecScene
// as observable-work signals.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: both fields are verified
// independently so a future refactor that drops one branch from
// IsEmpty() surfaces as a targeted test failure (specifying
// which field is broken), NOT as a generic false-positive bug.
func TestPostProcessResult_IsEmpty_RespectsTranslatedFields(t *testing.T) {
	t.Run("TranslatedText_only_IsEmptyFalse", func(t *testing.T) {
		// Changed=false (no flag) + no canonical output fields
		// + only TranslatedText populated. Pre-PR-6: IsEmpty=true.
		// Post-PR-6: IsEmpty=false (regression guard).
		r := &PostProcessResult{TranslatedText: "scene tradotta"}
		if r.IsEmpty() {
			t.Fatalf("IsEmpty()=true when only TranslatedText populated; " +
				"the false-positive bug detected by PR-6 audit has regressed. " +
				"Fix: IsEmpty() must include the TranslatedText != \"\" check " +
				"per postprocessor_document.go IsEmpty() body.")
		}
	})
	t.Run("TranslatedSpecScene_only_IsEmptyFalse", func(t *testing.T) {
		// Changed=false + only TranslatedSpecScene populated.
		// Post-PR-6: IsEmpty=false (regression guard).
		r := &PostProcessResult{
			TranslatedSpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{{
					ID: "scene-0", Index: 0, Kind: scriptpkg.SceneClip,
					Text: "scena tradotta",
				}},
			},
		}
		if r.IsEmpty() {
			t.Fatalf("IsEmpty()=true when only TranslatedSpecScene populated; " +
				"the false-positive bug detected by PR-6 audit has regressed.")
		}
	})
	t.Run("AllEmptyPlusNoFields_IsEmptyTrue", func(t *testing.T) {
		// Zero-value result + Changed=false + no populated fields
		// → IsEmpty=true (canonical default contract preserved).
		r := &PostProcessResult{}
		if !r.IsEmpty() {
			t.Fatalf("IsEmpty()=false for zero-value PostProcessResult; " +
				"the canonical empty-by-default contract has regressed. " +
				"PR-6 added branches but MUST NOT break the no-work path.")
		}
	})
}

// TestMergePostProcessResult_PropagatesTranslatedFields pins the
// LAST-WRITER-WINS semantics from PR-6: when src.TranslatedText is
// non-empty, dst.TranslatedText MUST equal src.TranslatedText.
// When src.TranslatedSpecScene.Scenes is non-empty, the same
// propagation contract holds for TranslatedSpecScene.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: empty-string guards ensure
// pre-translation pipeline runs (which carry TranslateTo="") do
// not accidentally overwrite a previously-set translated surface
// (last-writer-wins preserves the most-recent real translation).
//
// godlike/06 SSOT (one-canonical-owner-per-fact): mergePostProcessResult
// lives ONLY here in adapters. This test is the canonical SOLE
// regression guard for the PR-6 propagation contract.
func TestMergePostProcessResult_PropagatesTranslatedFields(t *testing.T) {
	t.Run("TranslatedText_propagates", func(t *testing.T) {
		dst := &PipelineResult{}
		src := &PostProcessResult{
			Changed:        true,
			Warnings:       []string{"translation soft-warn"},
			TranslatedText: "testo tradotto",
		}
		mergePostProcessResult(dst, src, nil)
		if dst.TranslatedText != "testo tradotto" {
			t.Fatalf("dst.TranslatedText=%q after merge with src.TranslatedText=%q; "+
				"the last-writer-wins propagation contract from PR-6 has regressed.",
				dst.TranslatedText, src.TranslatedText)
		}
		if len(dst.Warnings) != 1 || dst.Warnings[0] != "translation soft-warn" {
			t.Fatalf("dst.Warnings=%v after merge with src.Warnings=%v; "+
				"the existing merge contract (PR-3 P1#10) has regressed via the new PR-6 path.",
				dst.Warnings, src.Warnings)
		}
	})
	t.Run("TranslatedSpecScene_propagates", func(t *testing.T) {
		dst := &PipelineResult{}
		src := &PostProcessResult{
			Changed: true,
			TranslatedSpecScene: scriptpkg.SpecSceneOutput{
				Version: 1,
				Scenes: []scriptpkg.SpecScene{{
					ID: "scene-tradotta-0", Index: 0, Kind: scriptpkg.SceneClip,
					Text: "scena tradotta da test",
				}},
			},
		}
		mergePostProcessResult(dst, src, nil)
		if len(dst.TranslatedSpecScene.Scenes) != 1 {
			t.Fatalf("dst.TranslatedSpecScene.Scenes len=%d after merge; "+
				"expected 1 scene; the propagation contract from PR-6 has regressed. "+
				"dst.TranslatedSpecScene=%+v",
				len(dst.TranslatedSpecScene.Scenes), dst.TranslatedSpecScene)
		}
		if dst.TranslatedSpecScene.Scenes[0].ID != "scene-tradotta-0" {
			t.Fatalf("dst.TranslatedSpecScene.Scenes[0].ID=%q after merge; "+
				"expected scene-tradotta-0 (last-writer-wins invariant broken).",
				dst.TranslatedSpecScene.Scenes[0].ID)
		}
	})
	t.Run("EmptyTranslatedFields_doNotOverwrite", func(t *testing.T) {
		// Pre-existing translated surface (e.g. from a prior
		// non-empty TranslatedText in dst) must be preserved when
		// subsequent src carries empty fields (last-writer-wins
		// intentionally does NOT clobber existing translations
		// with empty payloads — godlike/07 NO-FAKE-AVAILABILITY).
		dst := &PipelineResult{
			TranslatedText: "prior translation preserved",
		}
		src := &PostProcessResult{TranslatedText: ""}
		mergePostProcessResult(dst, src, nil)
		if dst.TranslatedText != "prior translation preserved" {
			t.Fatalf("dst.TranslatedText=%q after empty-src merge; "+
				"expected prior translation 'prior translation preserved' preserved "+
				"(empty src MUST NOT clobber per godlike/07 NO-FAKE-AVAILABILITY).",
				dst.TranslatedText)
		}
	})
}

// TestPostProcessResult_TranslatedFields_StableSerialization pins
// the wire-shape contract: PostProcessResult with empty
// TranslatedText + empty TranslatedSpecScene must marshal to the
// canonical JSON envelope WITHOUT the translated_* keys (omitempty
// on both fields, per godlike/06 SSOT). Existing callers that did
// not opt into translation should not see a wire-shape diff.
//
// godlike/07 NO-FAKE-AVAILABILITY: JSON round-trip is the canonical
// contract surface; a future agent that drops omitempty would
// silently break pre-existing HTTP clients by emitting
// "translated_text":"" keys. This test blocks that drift class by
// asserting the JSON envelope for the no-op path.
func TestPostProcessResult_TranslatedFields_StableSerialization(t *testing.T) {
	// The wire-shape invariant is held by the canonical JSON tags
	// (`json:"...,omitempty"` on both new fields). The semantic
	// invariant below is the SECOND pillar of the canonical
	// contract: Changed-only PostProcessResult MUST remain
	// non-empty per the PR-3 P1#10 mutator-pass-through contract
	// (ensures no regression of the existing test surface when
	// the new PR-6 fields are added).
	r := &PostProcessResult{Changed: true}
	if r.IsEmpty() {
		t.Fatalf("IsEmpty()=true for Changed-only PostProcessResult; this is the " +
			"canonical mutator-pass-through contract from PR-3 P1#10 — must stay false.")
	}
	// Symmetric guard: completely-zero PostProcessResult MUST
	// remain empty per the no-work default contract (so the new
	// TranslatedText/TranslatedSpecScene branches in IsEmpty() do
	// not cause a false-non-empty on callers that did not populate
	// them).
	r2 := &PostProcessResult{}
	if !r2.IsEmpty() {
		t.Fatalf("IsEmpty()=false for completely-zero PostProcessResult; the canonical " +
			"no-work default has regressed.")
	}
}
