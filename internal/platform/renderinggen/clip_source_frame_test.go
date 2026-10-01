package renderinggen

import (
	"encoding/json"
	"os"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// sourceFramePlan builds a minimal sealed clip plan declaring the card treatment
// of the clip at the given foreground inset.
func sourceFramePlan(t *testing.T, frame *cliprender.PlanSourceFrame, foregroundScale int) cliprender.ClipRenderPlanV1 {
	t.Helper()
	dir := t.TempDir()
	sourcePath := dir + "/source.mp4"
	sourceBytes := []byte("source frame bytes")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "source-frame-clip-1",
		DurationMS: 4000,
		Source: cliprender.PlanSource{
			AssetID: "source-asset-001",
			Path:    sourcePath,
			SHA256:  digest.SHA256Bytes(sourceBytes),
		},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output: cliprender.PlanOutput{
			ContractID: cliprender.OutputContractVeloxAssemblyReadyV1, Container: "mp4",
			VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080,
			FPSNum: 24, FPSDen: 1, ForegroundScalePercent: foregroundScale,
			SourceFrame: frame,
		},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: dir + "/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal plan: %v", err)
	}
	return plan
}

// TestMapClipPlanToOverlayPlan_SourceFrame pins the producer half of the card
// contract: a declared frame must cross the queue verbatim (border + shadow),
// because the renderer owns the geometry and cannot invent a declaration the
// producer never made.
func TestMapClipPlanToOverlayPlan_SourceFrame(t *testing.T) {
	plan := sourceFramePlan(t, &cliprender.PlanSourceFrame{
		Border: &cliprender.PlanFrameBorder{WidthPX: 8, Color: "#FFFFFF", RadiusPX: 24},
		Shadow: &cliprender.PlanFrameShadow{Color: "#000000", Opacity: 0.5, BlurPX: 24, OffsetYP: 12},
	}, 70)

	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	var doc struct {
		ForegroundScale int               `json:"foreground_scale_percent"`
		SourceFrame     *sourceFrameBlock `json:"source_frame,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan: %v", err)
	}
	if doc.ForegroundScale != 70 {
		t.Errorf("foreground_scale_percent = %d, want 70", doc.ForegroundScale)
	}
	if doc.SourceFrame == nil || doc.SourceFrame.Border == nil || doc.SourceFrame.Shadow == nil {
		t.Fatalf("source_frame block = %+v, want border and shadow", doc.SourceFrame)
	}
	border := doc.SourceFrame.Border
	if border.WidthPX != 8 || border.Color != "#FFFFFF" || border.RadiusPX != 24 {
		t.Errorf("border = %+v, want the declared width/colour/radius", border)
	}
	shadow := doc.SourceFrame.Shadow
	if shadow.Color != "#000000" || shadow.Opacity != 0.5 || shadow.BlurPX != 24 || shadow.OffsetYP != 12 {
		t.Errorf("shadow = %+v, want the declared values", shadow)
	}
}

// TestMapClipPlanToOverlayPlan_NoSourceFrameStaysAbsent: the block is optional
// and must not be invented for a clip that declares none.
func TestMapClipPlanToOverlayPlan_NoSourceFrameStaysAbsent(t *testing.T) {
	plan := sourceFramePlan(t, nil, 80)
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	if bytes := string(raw); containsField(bytes, "source_frame") {
		t.Fatalf("undeclared source_frame must not appear on the wire:\n%s", bytes)
	}
}

// TestPlanSourceFrameValidationIsFailClosed pins the producer-side gate: a card
// the renderer could not honour is rejected before a job is queued.
func TestPlanSourceFrameValidationIsFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		frame  *cliprender.PlanSourceFrame
		scale  int
		reason string
	}{
		{
			name:   "empty block",
			frame:  &cliprender.PlanSourceFrame{},
			scale:  70,
			reason: "a frame with neither border nor shadow declares nothing",
		},
		{
			name: "border without an inset",
			frame: &cliprender.PlanSourceFrame{
				Border: &cliprender.PlanFrameBorder{WidthPX: 8, Color: "#FFFFFF"},
			},
			scale:  100,
			reason: "at 100% the frame sits entirely outside the canvas",
		},
		{
			name:   "default inset (unset)",
			frame:  &cliprender.PlanSourceFrame{Border: &cliprender.PlanFrameBorder{WidthPX: 8, Color: "#FFFFFF"}},
			scale:  0,
			reason: "an unset scale means full canvas, so the frame would be invisible",
		},
		{
			name:   "malformed colour",
			frame:  &cliprender.PlanSourceFrame{Border: &cliprender.PlanFrameBorder{WidthPX: 8, Color: "white"}},
			scale:  70,
			reason: "the render plan only parses #RRGGBB",
		},
		{
			name:   "shadow colour malformed",
			frame:  &cliprender.PlanSourceFrame{Shadow: &cliprender.PlanFrameShadow{Color: "#000"}},
			scale:  70,
			reason: "shorthand hex is not part of the contract",
		},
		{
			name:   "shadow blur out of range",
			frame:  &cliprender.PlanSourceFrame{Shadow: &cliprender.PlanFrameShadow{Color: "#000000", BlurPX: 4096}},
			scale:  70,
			reason: "blur is bounded by the published contract",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := sourceFramePlan(t, tc.frame, tc.scale)
			if err := plan.Validate(); err == nil {
				t.Fatalf("plan with %s must be rejected (%s)", tc.name, tc.reason)
			}
		})
	}
}

// TestPlanSourceFrameAcceptsAShadowWithoutAnInset: a clip shadow needs no
// border, so it is valid at full canvas.
func TestPlanSourceFrameAcceptsAShadowWithoutAnInset(t *testing.T) {
	plan := sourceFramePlan(t, &cliprender.PlanSourceFrame{
		Shadow: &cliprender.PlanFrameShadow{Color: "#000000", Opacity: 0.4, BlurPX: 20, OffsetYP: 8},
	}, 100)
	if err := plan.Validate(); err != nil {
		t.Fatalf("shadow-only frame at full canvas must validate: %v", err)
	}
}

func containsField(document, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		return false
	}
	_, ok := decoded[field]
	return ok
}
