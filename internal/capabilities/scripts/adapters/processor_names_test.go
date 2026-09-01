// Package adapters_test — processor_names_test.go exercises the
// typed ProcessorName constants and CanonicalProcessorNames().
//
// AZIONE 3 (July 2026): TDD test pins the 11-name closed set, the
// canonical execution order, and verifies no duplicates. Drift
// here (e.g. a constant renamed but CanonicalProcessorNames() not
// updated) surfaces as a test failure rather than a runtime
// "processor not registered" regression.
//
// PR-CLIP-SEARCH-WIRING (July 2026): bumped from 8 to 9 (added
// ProcessorClipSearch at index 1 — between entities and metadata —
// per the EXECUTION order documented in CanonicalProcessorNames).
// Clip is an OPTIONAL enrichment (BestEffort); when a plan does not
// request ExtractEntities the registry's run-path skips it, but
// the closed-set slot is reserved so the slice is deterministic.
//
// PR-TRANSLATE-SCRIPT-SPEC FP2 (2026-08-08): added ProcessorTranslation
// at index 3 — between metadata and clip_bindings — per the EXECUTION
// order documented in CanonicalProcessorNames. Translation is an
// OPTIONAL enrichment (BestEffort); when a plan does not request
// TranslateTo the registry's run-path skips it, but the closed-set
// slot is reserved so the slice is deterministic.
//
// PR-VIDRUSH-SEGMENT-IMAGES (2026-07-26): added ProcessorInternetImages
// at index 9 — after images and before persistence — to keep the web
// image provider in the canonical execution set without reusing the
// inline scene-image slot.
package adapters_test

import (
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
)

func TestCanonicalProcessorNames_ClosedSet(t *testing.T) {
	names := adapterspkg.CanonicalProcessorNames()

	// 1. Exactly 12 canonical names (PR-TRANSLATE-SCRIPT-SPEC FP2
	//    added translation at index 3; PR-CLIP-SEARCH-WIRING earlier
	//    added clip_search at index 1; stock_bindings added at
	//    index 5; internet_images added at index 9; document added
	//    at index 11; see file godoc above
	//    for EXECUTION vs REGISTRATION order distinction).
	if len(names) != 15 {
		t.Fatalf("CanonicalProcessorNames() returned %d names, want 15: %v", len(names), names)
	}

	// 2. Expected EXECUTION order (clip_search → metadata →
	//    translation → clip_bindings → stock_bindings → visual_planning →
	//    voiceover → images → internet_images → persistence). See processor_names.go
	//    goddoc for why this differs from the REGISTRATION order in
	//    internal/app/wire_script_postprocess.go.
	expected := []adapterspkg.ProcessorName{
		adapterspkg.ProcessorClipSearch,
		adapterspkg.ProcessorMetadata,
		adapterspkg.ProcessorTranslation,
		adapterspkg.ProcessorClipBindings,
		adapterspkg.ProcessorStockBindings,
		adapterspkg.ProcessorVisualPlanning,
		adapterspkg.ProcessorVisualSlots,
		adapterspkg.ProcessorVoiceover,
		adapterspkg.ProcessorImages,
		adapterspkg.ProcessorInternetImages,
		adapterspkg.ProcessorVidRushMaterialization,
		adapterspkg.ProcessorAssetLocationReconciliation,
		adapterspkg.ProcessorPersistence,
		adapterspkg.ProcessorDocument,
		adapterspkg.ProcessorNarrationSanitizer,
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("CanonicalProcessorNames()[%d] = %q, want %q", i, names[i], name)
		}
	}

	// 3. No duplicates.
	seen := make(map[adapterspkg.ProcessorName]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate in CanonicalProcessorNames(): %q", name)
		}
		seen[name] = true
	}
}

func TestProcessorName_StringConversion(t *testing.T) {
	// Verify round-trip: typed constant → string → typed constant.
	names := adapterspkg.CanonicalProcessorNames()
	strs := adapterspkg.ProcessorNamesToStrings(names)

	if len(strs) != len(names) {
		t.Fatalf("ProcessorNamesToStrings: len %d, want %d", len(strs), len(names))
	}
	for i, n := range names {
		got := string(n)
		want := strs[i]
		if got != want {
			t.Errorf("string(name[%d]) = %q, ProcessorNamesToStrings[%d] = %q", i, got, i, want)
		}
		if adapterspkg.ProcessorName(got) != n {
			t.Errorf("round-trip failed: ProcessorName(%q) != %q", got, n)
		}
	}
}

// TestCanonicalProcessorNames_IncludesClipSearch is the §2
// forward-prevention regression-guard for the PR-CLIP-SEARCH-WIRING
// (July 2026) addition. Pre-PR the canonical list was 8 names
// (no clip_search slot); post-PR it is 11 with clip_search at
// index 1 (between entities and metadata per the EXECUTION order
// documented in processor_names.go). This test pins BOTH the
// presence AND the canonical index so a future refactor that
// renames or removes clip_search surfaces as a build failure.
//
// godlike/06 SSOT: ProcessorClipSearch is the SOLE canonical home
// for the "clip_search" identifier (godlike/06 one-canonical-owner-per-fact)
// — declared in processor_names.go, NOT in processor_clip_search.go.
func TestCanonicalProcessorNames_IncludesClipSearch(t *testing.T) {
	names := adapterspkg.CanonicalProcessorNames()

	// 1. clip_search MUST be present in the canonical list.
	var found bool
	var foundIndex = -1
	for i, n := range names {
		if n == adapterspkg.ProcessorClipSearch {
			found = true
			foundIndex = i
			break
		}
	}
	if !found {
		t.Fatalf("CanonicalProcessorNames() does not contain ProcessorClipSearch (%q). "+
			"A future refactor that drops the clip_search slot would re-enable 'fuori registry' logic "+
			"in internal/application/scripts/usecase/generation_plan_builder.go. "+
			"If the slot was intentionally retired, update this test AND the goddoc in processor_names.go.",
			adapterspkg.ProcessorClipSearch)
	}

	// 2. clip_search MUST be at index 1 (between entities [0] and
	//    metadata [2]) per the EXECUTION order documented in
	//    processor_names.go godoc. A drift to a different index
	//    (e.g. moving clip_search to the tail) would silently
	//    break the ordering invariant: clip_search must run AFTER
	//    entities (reads Entities.ArtlistPhrases) and BEFORE
	//    metadata (so the enriched SpecScene text is visible).
	const wantIndex = 0
	if foundIndex != wantIndex {
		t.Errorf("ProcessorClipSearch is at index %d, want %d (before metadata per EXECUTION order). "+
			"A future refactor that moves clip_search would break the ordering invariant "+
			"documented in processor_names.go godoc.",
			foundIndex, wantIndex)
	}

	// 3. The string form MUST be "clip_search" (NOT "clip-search"
	//    or "ClipSearch" or "clips"). This pins the wire-shape
	//    that plan.Postprocessors serialises to JSON; a drift in
	//    the constant value would break the canonical contract
	//    with the engine dispatcher.
	if got := string(adapterspkg.ProcessorClipSearch); got != "clip_search" {
		t.Errorf("string(ProcessorClipSearch) = %q, want %q (canonical wire-shape invariant)", got, "clip_search")
	}

	// 4. The next canonical processor must be metadata, locking the
	//    execution order around the clip_search slot.
	if foundIndex < len(names)-1 && names[foundIndex+1] != adapterspkg.ProcessorMetadata {
		t.Errorf("names[%d] = %q, want %q (clip_search must precede metadata in EXECUTION order)",
			foundIndex+1, names[foundIndex+1], adapterspkg.ProcessorMetadata)
	}
}
