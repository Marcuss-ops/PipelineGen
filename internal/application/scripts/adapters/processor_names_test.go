// Package adapters_test — processor_names_test.go exercises the
// typed ProcessorName constants and CanonicalProcessorNames().
//
// AZIONE 3 (July 2026): TDD test pins the 8-name closed set, the
// canonical execution order, and verifies no duplicates. Drift
// here (e.g. a constant renamed but CanonicalProcessorNames() not
// updated) surfaces as a test failure rather than a runtime
// "processor not registered" regression.
package adapters_test

import (
	"testing"

	adapterspkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
)

func TestCanonicalProcessorNames_ClosedSet(t *testing.T) {
	names := adapterspkg.CanonicalProcessorNames()

	// 1. Exactly 8 canonical names.
	if len(names) != 8 {
		t.Fatalf("CanonicalProcessorNames() returned %d names, want 8: %v", len(names), names)
	}

	// 2. Expected order (entities → metadata → clip_bindings →
	//    stock_association → voiceover → images → document →
	//    persistence).
	expected := []adapterspkg.ProcessorName{
		adapterspkg.ProcessorEntities,
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
