package overlays

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func float64Ptr(value float64) *float64 { return &value }

// TestMergeImageStyleKeepsUnsetFieldsFromRunLevel pins the override contract: a
// per-pin style names only what is different, everything else keeps the
// run-level look (defaults included).
func TestMergeImageStyleKeepsUnsetFieldsFromRunLevel(t *testing.T) {
	base := &scriptpkg.OverlayImageStyleSpec{
		Radius: float64Ptr(24),
		Border: &scriptpkg.OverlayImageBorder{Width: 6, Color: "#22D3EE"},
		Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true, Color: "#000000", Blur: float64Ptr(10)},
		Glow:   &scriptpkg.OverlayImageGlowSpec{Radius: float64Ptr(18)},
		Effects: []scriptpkg.ImageEffectSpec{
			{Type: "glow", Radius: 12},
		},
		Grid: &scriptpkg.OverlayImageGridSpec{Enabled: boolPtr(true), Color: "#112233", SpacingPX: float64Ptr(48)},
	}
	merged := MergeImageStyle(base, &scriptpkg.OverlayImageStyleSpec{
		Grid:    &scriptpkg.OverlayImageGridSpec{Enabled: boolPtr(false)},
		Effects: []scriptpkg.ImageEffectSpec{{Type: "vignette", Amount: 0.4}},
	})
	if merged == nil {
		t.Fatal("merged style is nil")
	}
	if merged.Border != base.Border || merged.Shadow != base.Shadow || merged.Glow != base.Glow {
		t.Fatalf("pin style erased run-level frame fields: %+v", merged)
	}
	if merged.Radius == nil || *merged.Radius != 24 {
		t.Fatalf("pin style erased run-level radius: %+v", merged.Radius)
	}
	if len(merged.Effects) != 1 || merged.Effects[0].Type != "vignette" || merged.Effects[0].Amount != 0.4 {
		t.Fatalf("pin effects must replace the run-level stack: %+v", merged.Effects)
	}
	if merged.Grid == nil || merged.Grid.Enabled == nil || *merged.Grid.Enabled {
		t.Fatalf("pin grid.enabled=false was lost: %+v", merged.Grid)
	}
	if merged.Grid.Color != "#112233" || merged.Grid.SpacingPX == nil || *merged.Grid.SpacingPX != 48 {
		t.Fatalf("pin grid must keep the run-level keys it did not name: %+v", merged.Grid)
	}
	// The merge must not mutate the run-level declaration.
	if base.Grid.Enabled == nil || !*base.Grid.Enabled {
		t.Fatalf("merge mutated the run-level grid: %+v", base.Grid)
	}
	if len(base.Effects) != 1 || base.Effects[0].Type != "glow" {
		t.Fatalf("merge mutated the run-level effects: %+v", base.Effects)
	}
}

// TestMergeImageStyleNilCases pins the identity cases: no pin style keeps the
// run-level block untouched, no run-level block yields the pin block.
func TestMergeImageStyleNilCases(t *testing.T) {
	base := &scriptpkg.OverlayImageStyleSpec{Radius: float64Ptr(8)}
	pin := &scriptpkg.OverlayImageStyleSpec{Radius: float64Ptr(2)}
	if got := MergeImageStyle(base, nil); got != base {
		t.Fatalf("nil pin style must return the run-level block, got %+v", got)
	}
	if got := MergeImageStyle(nil, pin); got != pin {
		t.Fatalf("nil run-level style must return the pin block, got %+v", got)
	}
}

func boolPtr(value bool) *bool { return &value }
