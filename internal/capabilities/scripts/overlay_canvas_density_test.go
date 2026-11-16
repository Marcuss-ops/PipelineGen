package scriptgeneration

import (
	"strings"
	"testing"
)

func TestEditorialDensityPresetsFillOnlyUnsetCaps(t *testing.T) {
	minimal := (OverlayCanvasSpec{EditorialDensity: "minimal"}).withDefaults()
	if minimal.MaxPhraseOverlays != 3 || minimal.MaxImageOverlays != 4 ||
		minimal.MaxMapOverlays != 1 || minimal.MaxOverlaysPerScene != 1 {
		t.Fatalf("minimal preset = %+v", minimal)
	}

	dynamic := (OverlayCanvasSpec{EditorialDensity: "dynamic"}).withDefaults()
	if dynamic.MaxPhraseOverlays != 10 || dynamic.MaxImageOverlays != 12 ||
		dynamic.MaxMapOverlays != 2 || dynamic.MaxOverlaysPerScene != 3 {
		t.Fatalf("dynamic preset = %+v", dynamic)
	}

	explicit := (OverlayCanvasSpec{
		EditorialDensity:    "minimal",
		MaxPhraseOverlays:   7,
		MaxImageOverlays:    9,
		MaxMapOverlays:      2,
		MaxOverlaysPerScene: 4,
	}).withDefaults()
	if explicit.MaxPhraseOverlays != 7 || explicit.MaxImageOverlays != 9 ||
		explicit.MaxMapOverlays != 2 || explicit.MaxOverlaysPerScene != 4 {
		t.Fatalf("explicit caps were overwritten by the preset: %+v", explicit)
	}

	balanced := (OverlayCanvasSpec{EditorialDensity: "balanced"}).withDefaults()
	if balanced.MaxPhraseOverlays != 0 || balanced.MaxImageOverlays != 0 ||
		balanced.MaxMapOverlays != 0 || balanced.MaxOverlaysPerScene != 0 {
		t.Fatalf("balanced must keep the historical defaults, got %+v", balanced)
	}
}

func TestEditorialDensityFailsClosedOnUnknownValue(t *testing.T) {
	err := validateOverlayEditorialDensity(OverlayCanvasSpec{EditorialDensity: "cinematic"})
	if err == nil || !strings.Contains(err.Error(), "minimal, balanced or dynamic") {
		t.Fatalf("error=%v, want a fail-closed density error", err)
	}
	if err := validateOverlayEditorialDensity(OverlayCanvasSpec{}); err != nil {
		t.Fatalf("empty density must keep the historical defaults: %v", err)
	}
}

func TestEditorialDensityClampsThePhraseCapToTheHardLimit(t *testing.T) {
	got := (OverlayCanvasSpec{EditorialDensity: "dynamic", MaxPhraseOverlays: 999}).withDefaults()
	if got.MaxPhraseOverlays != 50 {
		t.Fatalf("phrase cap = %d, want the certified hard limit 50", got.MaxPhraseOverlays)
	}
}
