// Package render — transitions_test.go (PR7, June 2026).
//
// Concrete catalog tests moved from the application layer (stockpipeline/ports_test.go)
// alongside the DefaultTransitionRegistry implementation (transitions.go).
// Guards the 15-entry canonical catalog: count, names, insertion order,
// RenderEnd/RenderStart closures, duplicate-name handling, and Register extension.
package render

import (
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// TestDefaultTransitionCatalogCount asserts the canonical catalog has exactly
// 15 entries (14 named + 1 extra? no — 15 canonical transitions).
func TestDefaultTransitionCatalogCount(t *testing.T) {
	reg := DefaultTransitionRegistry()
	if reg.Len() != 15 {
		t.Fatalf("expected 15 canonical transitions, got %d", reg.Len())
	}
	all := reg.All()
	if len(all) != 15 {
		t.Fatalf("All returned %d entries, expected 15", len(all))
	}
}

// TestDefaultTransitionCatalogNames asserts every canonical name is present
// and every entry in the catalog is a canonical name (no extras).
func TestDefaultTransitionCatalogNames(t *testing.T) {
	reg := DefaultTransitionRegistry()
	canonicalNames := []string{
		"fadeblack", "fadewhite", "flash", "blur", "gray",
		"colorred", "colorblue", "colorgreen", "coloryellow",
		"colorpurple", "colororange", "colorpink",
		"negate", "vignette", "fastblur",
	}
	seen := make(map[string]bool, len(canonicalNames))
	for _, n := range canonicalNames {
		if _, ok := reg.Get(n); !ok {
			t.Fatalf("missing canonical transition: %q", n)
		}
		seen[n] = true
	}
	for _, tr := range reg.All() {
		if !seen[tr.Name] {
			t.Fatalf("non-canonical transition in catalog: %q", tr.Name)
		}
	}
}

// TestDefaultTransitionCatalogClosures asserts every entry has non-nil
// RenderEnd and RenderStart closures that produce non-empty strings.
func TestDefaultTransitionCatalogClosures(t *testing.T) {
	reg := DefaultTransitionRegistry()
	for _, tr := range reg.All() {
		if tr.RenderEnd == nil {
			t.Fatalf("transition %q missing RenderEnd closure", tr.Name)
		}
		if tr.RenderStart == nil {
			t.Fatalf("transition %q missing RenderStart closure", tr.Name)
		}
		// clipDur=4 is a representative value (catalog math depends on it for
		// fadeStart positioning — fadeStart = duration - 0.5).
		if tr.RenderEnd(4) == "" {
			t.Fatalf("RenderEnd(4) returned empty for %q", tr.Name)
		}
		if tr.RenderStart(4) == "" {
			t.Fatalf("RenderStart(4) returned empty for %q", tr.Name)
		}
	}
}

// TestDefaultTransitionCatalogInsertionOrder asserts All() returns entries
// in insertion order (not alphabetical). The first entry must be "fadeblack"
// and the second must be "fadewhite" (the historical rotation order).
func TestDefaultTransitionCatalogInsertionOrder(t *testing.T) {
	reg := DefaultTransitionRegistry()
	all := reg.All()
	if len(all) < 2 {
		t.Fatal("catalog too short for order test")
	}
	if all[0].Name != "fadeblack" {
		t.Fatalf("first entry should be fadeblack (insertion order), got %q", all[0].Name)
	}
	if all[1].Name != "fadewhite" {
		t.Fatalf("second entry should be fadewhite (insertion order), got %q", all[1].Name)
	}
}

// TestTransitionRegistryRegisterExtension asserts Register can add a new
// transition without disturbing existing entries.
func TestTransitionRegistryRegisterExtension(t *testing.T) {
	reg := DefaultTransitionRegistry()
	before := reg.Len()

	custom := stockpipeline.Transition{
		Name: "custom-xfade",
		RenderEnd: func(d int) string {
			return "xfade=transition=fadeblack:duration=0.5:offset=0"
		},
		RenderStart: func(d int) string {
			return "xfade=transition=fadeblack:duration=0.5:offset=0"
		},
	}

	// Register via the concrete type (same package, can see unexported type).
	if impl, ok := reg.(*inMemoryTransitionRegistry); ok {
		impl.Register(custom)
	}

	if reg.Len() != before+1 {
		t.Fatalf("expected %d after register, got %d", before+1, reg.Len())
	}
	if _, ok := reg.Get("custom-xfade"); !ok {
		t.Fatal("custom transition not retrievable after register")
	}
}

// TestTransitionRegistryRegisterDuplicateName asserts registering a
// transition with an existing name replaces it (no duplicate).
func TestTransitionRegistryRegisterDuplicateName(t *testing.T) {
	reg := DefaultTransitionRegistry()
	before := reg.Len()

	dupe := stockpipeline.Transition{
		Name: "fadeblack",
		RenderEnd: func(d int) string {
			return "custom-fadeblack-out"
		},
		RenderStart: func(d int) string {
			return "custom-fadeblack-in"
		},
	}

	if impl, ok := reg.(*inMemoryTransitionRegistry); ok {
		impl.Register(dupe)
	}

	if reg.Len() != before {
		t.Fatalf("registering duplicate name should not increase count: before=%d after=%d", before, reg.Len())
	}

	got, ok := reg.Get("fadeblack")
	if !ok {
		t.Fatal("fadeblack should still exist after duplicate register")
	}
	if got.RenderEnd(4) != "custom-fadeblack-out" {
		t.Fatalf("duplicate register did not replace RenderEnd: got %q", got.RenderEnd(4))
	}
	if got.RenderStart(4) != "custom-fadeblack-in" {
		t.Fatalf("duplicate register did not replace RenderStart: got %q", got.RenderStart(4))
	}
}

// TestDefaultTransitionRegistryConcurrentReads asserts All() and Get() are
// safe to call concurrently from multiple goroutines after construction.
func TestDefaultTransitionRegistryConcurrentReads(t *testing.T) {
	reg := DefaultTransitionRegistry()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				reg.All()
				reg.Get("fadeblack")
				reg.Get("vignette")
				reg.Len()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
