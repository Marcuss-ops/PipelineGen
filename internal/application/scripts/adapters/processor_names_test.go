// Package adapters_test — processor_names_test.go exercises the
// typed ProcessorName constants and CanonicalProcessorNames().
//
// AZIONE 3 (July 2026): TDD test pins the 9-name closed set, the
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
package adapters_test

import (
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

func TestCanonicalProcessorNames_ClosedSet(t *testing.T) {
	names := adapterspkg.CanonicalProcessorNames()

	// 1. Exactly 9 canonical names (PR-CLIP-SEARCH-WIRING added clip_search
	//    at index 1; see file godoc above for EXECUTION vs REGISTRATION
	//    order distinction).
	if len(names) != 9 {
		t.Fatalf("CanonicalProcessorNames() returned %d names, want 9: %v", len(names), names)
	}

	// 2. Expected EXECUTION order (entities → clip_search → metadata →
	//    clip_bindings → stock_association → voiceover → images →
	//    document → persistence). See processor_names.go goddoc for
	//    why this differs from the REGISTRATION order in
	//    internal/app/wire_script_postprocess.go.
	expected := []adapterspkg.ProcessorName{
		adapterspkg.ProcessorEntities,
		adapterspkg.ProcessorClipSearch,
		adapterspkg.ProcessorMetadata,
		adapterspkg.ProcessorClipBindings,
		adapterspkg.ProcessorStockAssociation,
		adapterspkg.ProcessorVoiceover,
		adapterspkg.ProcessorImages,
		adapterspkg.ProcessorDocument,
		adapterspkg.ProcessorPersistence,
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
