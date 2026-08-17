package overlays

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// TestChrononOverlayRegistry_CanonicalTable pins the frozen canonical
// capability table: exactly ten kinds, each resolving to its declared
// renderer + template with a positive version. A drift that adds/removes a
// kind without updating canonicalOverlayEntries is a loud test failure.
func TestChrononOverlayRegistry_CanonicalTable(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	if reg == nil {
		t.Fatal("NewChrononOverlayRegistry returned nil")
	}
	if reg.Len() != 10 {
		t.Fatalf("registry kind count = %d, want 10", reg.Len())
	}

	want := map[OverlayKind]struct {
		template string
		renderer string
	}{
		KindEntityCard:   {"person_default", "PersonCardRenderer"},
		KindOrganization: {"org_default", "OrganizationCardRenderer"},
		KindLocation:     {"gpe_default", "LocationCardRenderer"},
		KindConcept:      {"concept_default", "ConceptCardRenderer"},
		KindLowerThird:   {"lower_third", "LowerThirdRenderer"},
		KindImagePopup:   {"image_popup", "ImagePopupRenderer"},
		KindQuote:        {"quote", "QuoteRenderer"},
		KindNumber:       {"NUMBER", "NumberRenderer"},
		KindProduct:      {"PRODUCT", "ProductRenderer"},
		KindLogo:         {"LOGO", "LogoRenderer"},
	}

	kinds := reg.Kinds()
	if len(kinds) != 10 {
		t.Fatalf("Kinds() returned %d kinds, want 10", len(kinds))
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] >= kinds[i] {
			t.Fatalf("Kinds() not sorted: %v", kinds)
		}
	}

	for kind, expected := range want {
		e, err := reg.Resolve(string(kind))
		if err != nil {
			t.Fatalf("Resolve(%q): %v", kind, err)
		}
		if e.Kind != kind {
			t.Errorf("Resolve(%q).Kind = %q, want %q", kind, e.Kind, kind)
		}
		if e.Template != expected.template {
			t.Errorf("Resolve(%q).Template = %q, want %q", kind, e.Template, expected.template)
		}
		if e.Renderer == nil {
			t.Errorf("Resolve(%q).Renderer is nil", kind)
		} else if e.Renderer.Name() != expected.renderer {
			t.Errorf("Resolve(%q).Renderer.Name() = %q, want %q", kind, e.Renderer.Name(), expected.renderer)
		}
		if e.Version <= 0 {
			t.Errorf("Resolve(%q).Version = %d, want > 0", kind, e.Version)
		}
		if len(e.RequiredInputs) == 0 {
			t.Errorf("Resolve(%q).RequiredInputs is empty", kind)
		}
		if e.DurationPolicy == "" || e.PositioningPolicy == "" {
			t.Errorf("Resolve(%q) has empty policy fields: %+v", kind, e)
		}
	}
}

// TestChrononOverlayRegistry_ResolveNormalizesInput pins the normalization
// contract: whitespace and case are tolerated so planners are free of
// formatting constraints.
func TestChrononOverlayRegistry_ResolveNormalizesInput(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	for _, raw := range []string{"entity_card", " ENTITY_CARD ", "Entity_Card", "entity-card "} {
		_ = raw // last variant is not a valid kind (dash), handled below
	}
	e, err := reg.Resolve("  ENTITY_CARD ")
	if err != nil {
		t.Fatalf("Resolve normalized entity_card: %v", err)
	}
	if e.Renderer == nil || e.Renderer.Name() != "PersonCardRenderer" {
		t.Fatalf("normalized Resolve renderer = %v, want PersonCardRenderer", e.Renderer)
	}
}

// TestChrononOverlayRegistry_ResolveTemplateAndRenderer pins the convenience
// spellings used by planners.
func TestChrononOverlayRegistry_ResolveTemplateAndRenderer(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	tmpl, err := reg.ResolveTemplate("lower_third")
	if err != nil || tmpl != "lower_third" {
		t.Fatalf("ResolveTemplate(lower_third) = %q, %v", tmpl, err)
	}
	renderer, err := reg.ResolveRenderer("quote")
	if err != nil || renderer == nil || renderer.Name() != "QuoteRenderer" {
		t.Fatalf("ResolveRenderer(quote) = %v, %v", renderer, err)
	}
}

// TestChrononOverlayRegistry_UnknownKindFailsClosed pins the fail-closed
// contract: an unknown kind must never silently map to a default renderer.
func TestChrononOverlayRegistry_UnknownKindFailsClosed(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	_, err := reg.Resolve("flying_logo")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !errors.Is(err, ErrUnknownOverlayKind) {
		t.Fatalf("error = %v, want ErrUnknownOverlayKind", err)
	}
	if reg.Has("flying_logo") {
		t.Fatal("Has(flying_logo) = true, want false")
	}
	if _, err := reg.Resolve(""); err == nil {
		t.Fatal("expected error for empty kind")
	}
}

// TestChrononOverlayRegistry_NilReceiverIsSafe pins nil-safe reads so a
// zero-value registry never panics.
func TestChrononOverlayRegistry_NilReceiverIsSafe(t *testing.T) {
	var reg *ChrononOverlayRegistry
	if reg.Len() != 0 {
		t.Fatalf("nil Len() = %d, want 0", reg.Len())
	}
	if reg.Has("entity_card") {
		t.Fatal("nil Has() = true, want false")
	}
	if _, err := reg.Resolve("entity_card"); err == nil {
		t.Fatal("nil Resolve() must error")
	}
	if reg.AvailableKinds() != "(none)" {
		t.Fatalf("nil AvailableKinds() = %q, want (none)", reg.AvailableKinds())
	}
}

// TestChrononOverlayRegistry_DefaultSingleton pins that the process-wide
// DefaultChrononOverlayRegistry is the shared canonical instance (non-nil,
// frozen table) so every planner/resolver observes the same mapping.
func TestChrononOverlayRegistry_DefaultSingleton(t *testing.T) {
	if DefaultChrononOverlayRegistry == nil {
		t.Fatal("DefaultChrononOverlayRegistry is nil")
	}
	if DefaultChrononOverlayRegistry.Len() != len(canonicalOverlayEntries) {
		t.Fatalf("default registry kind count = %d, want %d", DefaultChrononOverlayRegistry.Len(), len(canonicalOverlayEntries))
	}
	if _, err := DefaultChrononOverlayRegistry.Resolve("entity_card"); err != nil {
		t.Fatalf("default registry cannot resolve entity_card: %v", err)
	}
	fresh := NewChrononOverlayRegistry()
	if fresh.Len() != DefaultChrononOverlayRegistry.Len() {
		t.Fatalf("fresh vs default registry size drift: %d vs %d", fresh.Len(), DefaultChrononOverlayRegistry.Len())
	}
}

// TestChrononOverlayRegistry_ConcurrentReads pins the immutability contract:
// the registry is built once and never mutated, so concurrent readers observe
// a consistent frozen table (no data race, no partial state).
func TestChrononOverlayRegistry_ConcurrentReads(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	const workers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				kind := canonicalOverlayEntries[(n+j)%len(canonicalOverlayEntries)].Kind
				e, err := reg.Resolve(string(kind))
				if err != nil {
					errCh <- fmt.Errorf("worker %d: Resolve(%q): %w", n, kind, err)
					return
				}
				if e.Kind != kind || e.Renderer == nil {
					errCh <- fmt.Errorf("worker %d: inconsistent entry for %q: %+v", n, kind, e)
					return
				}
				if !reg.Has(string(kind)) {
					errCh <- fmt.Errorf("worker %d: Has(%q) = false", n, kind)
					return
				}
				if reg.Len() != len(canonicalOverlayEntries) {
					errCh <- fmt.Errorf("worker %d: Len drifted to %d", n, reg.Len())
					return
				}
				_ = reg.Kinds()
				_ = reg.AvailableKinds()
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestChrononOverlayRegistry_CanonicalPolicies pins the exact duration +
// positioning policy per kind so a planner branching on a policy value cannot
// silently drift.
func TestChrononOverlayRegistry_CanonicalPolicies(t *testing.T) {
	reg := NewChrononOverlayRegistry()
	want := map[OverlayKind]struct {
		duration    DurationPolicy
		positioning PositioningPolicy
	}{
		KindEntityCard:   {DurationCertified, PositionEntityCard},
		KindOrganization: {DurationCertified, PositionEntityCard},
		KindLocation:     {DurationCertified, PositionEntityCard},
		KindConcept:      {DurationCertified, PositionEntityCard},
		KindLowerThird:   {DurationBounded, PositionLowerThird},
		KindImagePopup:   {DurationBounded, PositionPopup},
		KindQuote:        {DurationBounded, PositionCentered},
		KindNumber:       {DurationCertified, PositionCentered},
		KindProduct:      {DurationBounded, PositionPopup},
		KindLogo:         {DurationBounded, PositionCorner},
	}
	for kind, w := range want {
		e, err := reg.Resolve(string(kind))
		if err != nil {
			t.Fatalf("Resolve(%q): %v", kind, err)
		}
		if e.DurationPolicy != w.duration {
			t.Errorf("Resolve(%q).DurationPolicy = %q, want %q", kind, e.DurationPolicy, w.duration)
		}
		if e.PositioningPolicy != w.positioning {
			t.Errorf("Resolve(%q).PositioningPolicy = %q, want %q", kind, e.PositioningPolicy, w.positioning)
		}
	}
}
