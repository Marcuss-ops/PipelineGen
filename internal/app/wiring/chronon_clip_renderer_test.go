package wiring

import (
	"context"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingProbe returns a fixed capability set (and no error).
type recordingProbe struct {
	caps cliprender.RendererCapabilities
}

func (p recordingProbe) ProbeCapabilities(context.Context) (cliprender.RendererCapabilities, error) {
	return p.caps, nil
}

// TestChrononAwareCapabilityProbe_ReportsChrononOnlyWhenBinaryConfigured
// verifies the probe wrapper is honest: chronon_vulkan is probed ONLY when a
// Chronon render binary is configured, so the resolver can select the Chronon
// backend without an adapter-level shortcut.
func TestChrononAwareCapabilityProbe_ReportsChrononOnlyWhenBinaryConfigured(t *testing.T) {
	base := recordingProbe{caps: cliprender.RendererCapabilities{NVDEC: true, NVENCH264: true}}

	withBin := chrononAwareCapabilityProbe{base: base, chrononBin: "/opt/chronon3d/bin/chronon3d_cli"}
	caps, err := withBin.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if !caps.ChrononVulkan {
		t.Fatal("chronon_vulkan must be reported when the binary is configured")
	}
	if !caps.NVDEC || !caps.NVENCH264 {
		t.Fatalf("base capabilities must pass through, got %+v", caps)
	}

	withoutBin := chrononAwareCapabilityProbe{base: base, chrononBin: ""}
	caps, err = withoutBin.ProbeCapabilities(context.Background())
	if err != nil {
		t.Fatalf("ProbeCapabilities: %v", err)
	}
	if caps.ChrononVulkan {
		t.Fatal("chronon_vulkan must NOT be reported when no binary is configured")
	}
}

var _ cliprender.BackendCapabilityProbe = (*chrononAwareCapabilityProbe)(nil)

// TestProjectLayerStyle_ProjectsCanonicalStyle verifies the canonical
// VideoVisualStyleSpec (the exact block script.generate accepts) is lowered
// onto the typed Chronon layer style (fill/shadow/font_size). Only declared
// fields are emitted — no fake defaults.
func TestProjectLayerStyle_ProjectsCanonicalStyle(t *testing.T) {
	style := &scriptpkg.VideoVisualStyleSpec{
		Color:      "#FFFFFF",
		FontSizePX: 54,
		Shadow: &scriptpkg.VideoShadowSpec{
			Color:   "#000000",
			Opacity: 0.7,
			BlurPX:  10,
			OffsetX: 0,
			OffsetY: 5,
		},
	}
	block := projectLayerStyle(style)
	if block == nil {
		t.Fatal("style = nil, want the projected style")
	}
	if block.Fill != "#FFFFFF" {
		t.Errorf("fill = %q, want #FFFFFF", block.Fill)
	}
	if block.FontSize != 54.0 {
		t.Errorf("font_size = %v, want 54", block.FontSize)
	}
	if block.Shadow == nil {
		t.Fatal("shadow = nil, want the projected shadow")
	}
	if block.Shadow.Color != "#000000" || block.Shadow.Opacity != 0.7 || block.Shadow.Blur != 10.0 {
		t.Errorf("shadow = %+v, want color/opacity/blur", block.Shadow)
	}
	if len(block.Shadow.Offset) != 2 || block.Shadow.Offset[0] != 0 || block.Shadow.Offset[1] != 5 {
		t.Errorf("offset = %v, want [0 5]", block.Shadow.Offset)
	}
}

// TestProjectLayerStyle_PartialAndAbsent verifies only declared fields are
// emitted: a shadow with just a color carries no fabricated defaults, and a
// nil/empty style produces NO style block at all.
func TestProjectLayerStyle_PartialAndAbsent(t *testing.T) {
	partial := &scriptpkg.VideoVisualStyleSpec{
		Color: "#FFC107",
		Shadow: &scriptpkg.VideoShadowSpec{
			Color: "#000000",
		},
	}
	block := projectLayerStyle(partial)
	if block.Fill != "#FFC107" {
		t.Errorf("fill = %q, want #FFC107", block.Fill)
	}
	if block.FontSize != 0 {
		t.Errorf("font_size = %v, want 0 (not declared)", block.FontSize)
	}
	if block.Shadow.Color != "#000000" {
		t.Errorf("shadow color = %q, want #000000", block.Shadow.Color)
	}
	if block.Shadow.Opacity != 0 || block.Shadow.Blur != 0 || block.Shadow.Offset != nil {
		t.Errorf("undeclared shadow fields emitted: %+v", block.Shadow)
	}

	if got := projectLayerStyle(nil); got != nil {
		t.Errorf("nil style → %+v, want nil", got)
	}
	if got := projectLayerStyle(&scriptpkg.VideoVisualStyleSpec{}); got != nil {
		t.Errorf("empty style → %+v, want nil", got)
	}
	// A declared (0,0) offset is the default — it must not fabricate an
	// offset.
	twoDim := &scriptpkg.VideoVisualStyleSpec{
		Shadow: &scriptpkg.VideoShadowSpec{Color: "#000000", OffsetX: 0, OffsetY: 0},
	}
	if s := projectLayerStyle(twoDim); s.Shadow.Offset != nil {
		t.Errorf("zero offset emitted: %v", s.Shadow.Offset)
	}
}
