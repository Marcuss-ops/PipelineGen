package overlay

import (
	"strings"
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// TestResolveMapEncodeProfileDefaultsToNativeCPU pins the output contract:
// with no explicit declaration the profile is the single native renderer's
// certified closed profile (libx264/veryfast/CRF23). The Python renderer is
// retired (TICKET-MAPS-NATIVE-PORT fase 7) and the native renderer has no
// OpenCL sampler, so there is no GPU default to preserve.
func TestResolveMapEncodeProfileDefaultsToNativeCPU(t *testing.T) {
	profile, err := ResolveMapEncodeProfile("")
	if err != nil {
		t.Fatalf("empty declaration must select the production default: %v", err)
	}
	if profile.Codec != MapCPUCodec || profile.ExpectGPU || profile.Preset != "veryfast" || profile.Quality != 23 {
		t.Fatalf("default profile = %+v, want the native certified CPU profile libx264 veryfast/CRF23", profile)
	}
	if err := RequireNativeServableProfile(profile); err != nil {
		t.Fatalf("the default profile must be servable by the native renderer: %v", err)
	}
	explicit, err := ResolveMapEncodeProfile("libx264")
	if err != nil || explicit != profile {
		t.Fatalf("explicit CPU codec must equal the default: %+v, %v", explicit, err)
	}
}

// TestResolveMapEncodeProfileKeepsTheClosedGPUContract pins the second
// declarable profile: the legacy NVENC family stays declarable with its exact
// wire shaping (the contract a future OpenCL-sampler port must satisfy), but
// the render boundary rejects it typed until that port lands — never a silent
// downgrade to a CPU encode, never a silent acceptance.
func TestResolveMapEncodeProfileKeepsTheClosedGPUContract(t *testing.T) {
	gpu, err := ResolveMapEncodeProfile(MapLegacyGPUCodec)
	if err != nil {
		t.Fatalf("the legacy NVENC profile must stay declarable: %v", err)
	}
	if gpu.Codec != "h264_nvenc" || !gpu.ExpectGPU || gpu.Preset != "p6" || gpu.Quality != 15 {
		t.Fatalf("legacy GPU profile = %+v, want the closed NVENC p6/CQ15 contract", gpu)
	}
	if err := RequireNativeServableProfile(gpu); err == nil {
		t.Fatal("the legacy NVENC profile must fail closed at the native render boundary (OpenCL sampler demolition debt)")
	}
	if _, err := ResolveMapEncodeProfile("hevc_nvenc"); err == nil {
		t.Fatal("an uncertified encoder name must fail closed")
	}
	if _, err := ResolveMapEncodeProfile("auto"); err == nil {
		t.Fatal("an \"auto\" placeholder must fail closed instead of picking an encoder silently")
	}
}

// TestMapEncodeVideoArgsCarryTheDeclaredCodec proves the ffmpeg shaping comes
// from the declared profile: the CPU branch is the production shaping, the
// closed GPU branch keeps its exact NVENC shaping byte-identical for the
// future port and never leaks GPU flags into CPU args.
func TestMapEncodeVideoArgsCarryTheDeclaredCodec(t *testing.T) {
	cpu := DefaultMapEncodeProfile()
	cpuJoined := strings.Join(MapEncodeVideoArgs(cpu), " ")
	for _, want := range []string{"-c:v libx264", "-preset veryfast", "-crf 23"} {
		if !strings.Contains(cpuJoined, want) {
			t.Fatalf("CPU args %q miss declared option %q", cpuJoined, want)
		}
	}
	for _, forbidden := range []string{"nvenc", "-gpu", "-tune hq", "-cq"} {
		if strings.Contains(cpuJoined, forbidden) {
			t.Fatalf("CPU args %q must not carry GPU shaping %q", cpuJoined, forbidden)
		}
	}
	gpu, err := ResolveMapEncodeProfile(MapLegacyGPUCodec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(MapEncodeVideoArgs(gpu), " ")
	for _, want := range []string{"-c:v h264_nvenc", "-gpu 0", "-preset p6", "-tune hq", "-rc vbr", "-cq 15", "-b:v 0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("legacy GPU args %q miss %q", joined, want)
		}
	}
}

// TestValidateMapEncoderTelemetryRefusesAnotherProfile pins the decoupling:
// telemetry from a different profile than declared is a certification error
// naming both sides — never a silent acceptance, never an auto-substitution.
func TestValidateMapEncoderTelemetryRefusesAnotherProfile(t *testing.T) {
	cpu := DefaultMapEncodeProfile()
	gpu, err := ResolveMapEncodeProfile(MapLegacyGPUCodec)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMapEncoderTelemetry("libx264", false, "veryfast", 23, cpu); err != nil {
		t.Fatalf("matching CPU telemetry must certify: %v", err)
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, "p6", 15, cpu); err == nil {
		t.Fatal("NVENC telemetry under the CPU profile must fail (no silent NVENC→x264 swap)")
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, "p6", 15, gpu); err != nil {
		t.Fatalf("matching legacy GPU telemetry must certify: %v", err)
	}
	if err := ValidateMapEncoderTelemetry("libx264", false, "veryfast", 23, gpu); err == nil {
		t.Fatal("CPU telemetry under the GPU profile must fail")
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", false, "p6", 15, gpu); err == nil {
		t.Fatal("a GPU codec without the GPU flag must fail (flag and codec travel together)")
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, "p4", 15, gpu); err == nil {
		t.Fatal("NVENC telemetry with a different preset must fail")
	}
	if err := ValidateMapEncoderTelemetry("h264_nvenc", true, "p6", 18, gpu); err == nil {
		t.Fatal("NVENC telemetry with a different quality must fail")
	}
}

// TestMapDiagnosticCannotForceNativeForDynamicContent is the parity gate:
// the native MAP compiler has no tile pyramid, label animations or editorial
// compositor. A composition needing unported capabilities fails closed with a
// typed error — it is never silently downgraded to a backend that cannot draw
// it. The diagnostic opt-in only ever applies to static content.
func TestMapDiagnosticCannotForceNativeForDynamicContent(t *testing.T) {
	dynamic := capabilityoverlay.OverlayItem{ID: "map-1", Kind: "map",
		Map:    &capabilityoverlay.MapOverlay{},
		Params: map[string]any{"map_animation": "region_zoom", "map_pin_timing": map[string]any{}},
	}
	if backend, _, err := ResolveMapItemBackend(dynamic, true); err == nil && backend == MapBackendNative {
		t.Fatalf("unported dynamic content must never resolve to the native compiler (got %q)", backend)
	}
	if _, _, err := ResolveMapItemBackend(dynamic, true); err == nil {
		t.Fatal("unported dynamic content must fail closed until it is ported")
	}
	static := capabilityoverlay.OverlayItem{ID: "map-2", Kind: "map",
		Map: &capabilityoverlay.MapOverlay{},
	}
	backend, _, err := ResolveMapItemBackend(static, true)
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
	if backend != MapBackendDynamic {
		t.Fatalf("production keeps the certified dynamic presentation = %q, want dynamic", backend)
	}
}
