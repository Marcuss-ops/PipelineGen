package overlay

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestResolveMapEncodeProfileDefaultsToProduction pins the output contract:
// with no explicit declaration the profile requires the production NVENC
// encoder. Existing jobs keep their exact codec without any operator action.
func TestResolveMapEncodeProfileDefaultsToProduction(t *testing.T) {
	profile, err := ResolveMapEncodeProfile("")
	if err != nil {
		t.Fatalf("empty declaration must select production: %v", err)
	}
	if profile.Codec != MapProductionCodec || !profile.ExpectGPU {
		t.Fatalf("default profile = %+v, want production NVENC with GPU", profile)
	}
	if profile.Codec != "h264_nvenc" {
		t.Fatalf("production codec = %q, want h264_nvenc (existing jobs keep NVENC)", profile.Codec)
	}
	explicit, err := ResolveMapEncodeProfile("h264_nvenc")
	if err != nil || explicit != profile {
		t.Fatalf("explicit production codec must equal the default: %+v, %v", explicit, err)
	}
}

// TestResolveMapEncodeProfileCertifiesCPUOnlyOnExplicitOptIn pins the second
// certified path: libx264 is available but never implicit, and unknown names
// fail closed instead of degrading to whatever encoder is at hand.
func TestResolveMapEncodeProfileCertifiesCPUOnlyOnExplicitOptIn(t *testing.T) {
	profile, err := ResolveMapEncodeProfile("libx264")
	if err != nil {
		t.Fatalf("explicit CPU codec must resolve: %v", err)
	}
	if profile.Codec != MapCPUCodec || profile.ExpectGPU {
		t.Fatalf("CPU profile = %+v, want libx264 without GPU", profile)
	}
	if _, err := ResolveMapEncodeProfile("hevc_nvenc"); err == nil {
		t.Fatal("an uncertified encoder name must fail closed")
	}
	if _, err := ResolveMapEncodeProfile("auto"); err == nil {
		t.Fatal("an \"auto\" placeholder must fail closed instead of picking an encoder silently")
	}
}

// TestMapEncodeVideoArgsCarryTheDeclaredCodec proves the ffmpeg shaping comes
// from the declared profile: the production branch keeps the certified NVENC
// shaping byte-identical, the CPU branch never smuggles NVENC flags in.
func TestMapEncodeVideoArgsCarryTheDeclaredCodec(t *testing.T) {
	gpu := MapEncodeVideoArgs(DefaultMapEncodeProfile())
	joined := strings.Join(gpu, " ")
	for _, want := range []string{"-c:v h264_nvenc", "-preset p4", "-tune hq", "-rc vbr", "-cq 18"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("production args %q miss %q", joined, want)
		}
	}
	cpu, err := ResolveMapEncodeProfile("libx264")
	if err != nil {
		t.Fatal(err)
	}
	cpuArgs := MapEncodeVideoArgs(cpu)
	cpuJoined := strings.Join(cpuArgs, " ")
	if !strings.Contains(cpuJoined, "-c:v libx264") {
		t.Fatalf("CPU args %q must carry the declared codec", cpuJoined)
	}
	for _, forbidden := range []string{"nvenc", "-gpu", "-tune hq", "-cq"} {
		if strings.Contains(cpuJoined, forbidden) {
			t.Fatalf("CPU args %q must not carry GPU shaping %q", cpuJoined, forbidden)
		}
	}
}

// TestValidateMapEncoderTelemetryRefusesAnotherProfile pins the decoupling:
// telemetry from a different profile than declared is a certification error
// naming both sides — never a silent acceptance, never an auto-substitution.
func TestValidateMapEncoderTelemetryRefusesAnotherProfile(t *testing.T) {
	production := DefaultMapEncodeProfile()
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, production); err != nil {
		t.Fatalf("matching production telemetry must certify: %v", err)
	}
	if err := ValidateMapEncoderTelemetry("libx264", false, production); err == nil {
		t.Fatal("CPU telemetry under the production profile must fail (no silent NVENC→x264 swap)")
	}
	cpu, _ := ResolveMapEncodeProfile("libx264")
	if err := ValidateMapEncoderTelemetry("libx264", false, cpu); err != nil {
		t.Fatalf("matching CPU telemetry must certify: %v", err)
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, cpu); err == nil {
		t.Fatal("NVENC telemetry under the CPU profile must fail")
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", false, production); err == nil {
		t.Fatal("a GPU codec without the GPU flag must fail (flag and codec travel together)")
	}
}

// TestMapDiagnosticCannotForceNativeForDynamicContent is the parity gate: the
// native MAP compiler has no tile pyramid, label animations or editorial
// compositor, so visual parity with the dynamic renderer is NOT established.
// The diagnostic opt-in therefore only ever applies to static content — a
// composition needing dynamic-only capabilities still resolves to the dynamic
// backend. Removing the native camera gap (true visual parity) is what would
// allow this gate to open, never a flag flip.
func TestMapDiagnosticCannotForceNativeForDynamicContent(t *testing.T) {
	dynamic := capabilityoverlay.OverlayItem{ID: "map-1", Kind: "map",
		Map:    &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_animation": "region_zoom", "map_pin_timing": map[string]any{}},
	}
	backend, _, err := ResolveMapItemBackend(dynamic, true)
	if err != nil {
		t.Fatalf("dynamic requirements must resolve: %v", err)
	}
	if backend != MapBackendPython {
		t.Fatalf("diagnostic native render of dynamic content = %q, want python until visual parity exists", backend)
	}
	static := capabilityoverlay.OverlayItem{ID: "map-2", Kind: "map",
		Map: &capabilityoverlay.MapOverlay{},
	}
	backend, _, err = ResolveMapItemBackend(static, true)
	if err != nil {
		t.Fatalf("static requirements must resolve: %v", err)
	}
	if backend != MapBackendNative {
		t.Fatalf("diagnostic native render of static content = %q, want native", backend)
	}
	backend, _, err = ResolveMapItemBackend(static, false)
	if err != nil {
		t.Fatalf("static requirements must resolve: %v", err)
	}
	if backend != MapBackendPython {
		t.Fatalf("production (no diagnostic) keeps the certified dynamic presentation = %q, want python", backend)
	}
}
