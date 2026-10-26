package overlay

import (
	"fmt"
	"strings"
)

// MapEncodeProfile is the DECLARED execution profile for a map render: which
// video codec the artifact must carry and whether a GPU encoder is expected.
//
// Cartographic validity and encoder choice are separate concerns: the
// observed telemetry must match the declared profile. A mismatch fails
// closed — the boundary never silently substitutes one codec for another.
// Since the Python renderer was retired (TICKET-MAPS-NATIVE-PORT fase 7) the
// single map lane is the native renderer of ChrononPlugin, whose certified
// closed profile is libx264/veryfast/CRF23: the NVENC family needs the
// OpenCL sampler the native renderer does not provide yet (demolition debt),
// so an NVENC declaration is rejected at the render boundary with a typed
// error instead of being silently downgraded to a CPU encode.
type MapEncodeProfile struct {
	// Codec is the required video encoder (telemetry video_encoder).
	Codec string
	// ExpectGPU is the required telemetry gpu_encoder flag.
	ExpectGPU bool
	// Preset and Quality are the declared encoder tuning values. Quality maps
	// to NVENC CQ for GPU profiles and CRF for CPU profiles.
	Preset  string
	Quality int
}

const (
	// MapProductionCodec is the certified production map encoder: the native
	// renderer's closed CPU profile (libx264/veryfast/CRF23).
	MapProductionCodec = MapCPUCodec
	// MapLegacyGPUCodec is the GPU NVENC profile of the retired Python lane.
	// It stays declarable and its shaping contract remains pinned below, but
	// the render boundary rejects it until the OpenCL sampler is ported.
	MapLegacyGPUCodec = "h264_nvenc"
	// MapCPUCodec is the native renderer's certified CPU map encoder.
	MapCPUCodec = "libx264"
	// MapEncodeProfileEnv names the explicit profile declaration. Empty means
	// the production default; naming the CPU codec certifies the CPU path.
	// Anything else fails closed.
	MapEncodeProfileEnv = "PIPELINEGEN_MAP_CODEC"
)

// DefaultMapEncodeProfile is the production contract: the native renderer's
// certified CPU profile at preset veryfast/CRF 23.
func DefaultMapEncodeProfile() MapEncodeProfile {
	return MapEncodeProfile{Codec: MapCPUCodec, ExpectGPU: false, Preset: "veryfast", Quality: 23}
}

// ResolveMapEncodeProfile declares the execution profile for one map render.
// Empty selects the production default; the CPU codec selects the same
// certified CPU path explicitly; the legacy NVENC codec resolves as the
// closed GPU profile the render boundary rejects (never silently encoded on
// another encoder). An unknown name fails closed instead of degrading to
// whichever encoder happens to be available.
func ResolveMapEncodeProfile(explicit string) (MapEncodeProfile, error) {
	switch strings.TrimSpace(strings.ToLower(explicit)) {
	case "":
		return DefaultMapEncodeProfile(), nil
	case MapCPUCodec:
		return DefaultMapEncodeProfile(), nil
	case MapLegacyGPUCodec:
		return MapEncodeProfile{Codec: MapLegacyGPUCodec, ExpectGPU: true, Preset: "p6", Quality: 15}, nil
	default:
		return MapEncodeProfile{}, fmt.Errorf("unsupported map encoder %q: declare %q (production) or %q (legacy NVENC, rejected until the OpenCL sampler is ported)",
			strings.TrimSpace(explicit), MapCPUCodec, MapLegacyGPUCodec)
	}
}

// RequireNativeServableProfile fails closed on a declared profile the single
// native map renderer cannot execute. The NVENC family needs the OpenCL
// sampler (demolition debt): it is rejected with a typed error naming the
// unported capability — never silently encoded on another encoder.
func RequireNativeServableProfile(profile MapEncodeProfile) error {
	if profile.ExpectGPU {
		return fmt.Errorf("map encoder %q requires the OpenCL sampler the native map renderer does not provide yet (demolition debt, TICKET-MAPS-NATIVE-PORT): declare %q or leave %s empty",
			profile.Codec, MapCPUCodec, MapEncodeProfileEnv)
	}
	return nil
}

// MapEncodeVideoArgs renders the declared profile as ffmpeg video-encoder
// arguments. The codec, preset and quality are one contract shared with the
// Python renderer and any later upscale; callers never restate encoder knobs.
func MapEncodeVideoArgs(profile MapEncodeProfile) []string {
	quality := fmt.Sprint(profile.Quality)
	if profile.ExpectGPU {
		return []string{"-c:v", profile.Codec, "-gpu", "0", "-preset", profile.Preset, "-tune", "hq", "-rc", "vbr", "-cq", quality, "-b:v", "0"}
	}
	return []string{"-c:v", profile.Codec, "-preset", profile.Preset, "-crf", quality}
}

// ValidateMapEncoderTelemetry certifies that the renderer's observed encoder
// matches the declared execution profile. A render produced under another
// profile is rejected with both sides named — never silently accepted.
func ValidateMapEncoderTelemetry(encoder string, gpu bool, preset string, quality int, profile MapEncodeProfile) error {
	if strings.TrimSpace(encoder) != profile.Codec || gpu != profile.ExpectGPU ||
		strings.TrimSpace(preset) != profile.Preset || quality != profile.Quality {
		return fmt.Errorf("dynamic map renderer used encoder=%q gpu=%t preset=%q quality=%d, but the declared execution profile requires encoder=%q gpu=%t preset=%q quality=%d",
			encoder, gpu, preset, quality, profile.Codec, profile.ExpectGPU, profile.Preset, profile.Quality)
	}
	return nil
}
