package overlays

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
// The native vector plate sampler still builds raster frames on CPU, while
// NVENC handles video encoding on GPU. Those are independent stages: lack of
// an OpenCL sampler must not force the encoder back to libx264.
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
	// MapProductionCodec is the production NVENC encoder; map rasterization is
	// currently CPU sampled, then the frames are encoded on GPU.
	MapProductionCodec = "h264_nvenc"
	// MapLegacyGPUCodec is retained as a source-compatible alias.
	MapLegacyGPUCodec = MapProductionCodec
	// MapCPUCodec is the native renderer's certified CPU map encoder.
	MapCPUCodec = "libx264"
	// MapEncodeProfileEnv names the explicit profile declaration. Empty means
	// the production default; naming the CPU codec certifies the CPU path.
	// Anything else fails closed.
	MapEncodeProfileEnv = "PIPELINEGEN_MAP_CODEC"
)

// DefaultMapEncodeProfile sends the encoding stage to NVENC.
func DefaultMapEncodeProfile() MapEncodeProfile {
	return MapEncodeProfile{Codec: MapProductionCodec, ExpectGPU: true, Preset: "p6", Quality: 15}
}

// ResolveMapEncodeProfile declares the execution profile for one map render.
// Empty selects the production default; the CPU codec selects the same
// certified CPU path explicitly; the legacy NVENC codec resolves as the
// GPU profile. An unknown name fails closed instead of degrading to
// whichever encoder happens to be available.
func ResolveMapEncodeProfile(explicit string) (MapEncodeProfile, error) {
	switch strings.TrimSpace(strings.ToLower(explicit)) {
	case "":
		return DefaultMapEncodeProfile(), nil
	case MapCPUCodec:
		return MapEncodeProfile{Codec: MapCPUCodec, ExpectGPU: false, Preset: "veryfast", Quality: 23}, nil
	case MapLegacyGPUCodec:
		return MapEncodeProfile{Codec: MapLegacyGPUCodec, ExpectGPU: true, Preset: "p6", Quality: 15}, nil
	default:
		return MapEncodeProfile{}, fmt.Errorf("unsupported map encoder %q: declare %q (production) or %q (CPU diagnostic)",
			strings.TrimSpace(explicit), MapCPUCodec, MapLegacyGPUCodec)
	}
}

// RequireNativeServableProfile validates that the native renderer can execute
// the declared encoder. CPU sampling and GPU encoding are independent choices.
func RequireNativeServableProfile(profile MapEncodeProfile) error {
	if profile.ExpectGPU && profile.Codec != MapLegacyGPUCodec {
		return fmt.Errorf("unsupported GPU map encoder %q", profile.Codec)
	}
	return nil
}

// MapEncodeVideoArgs renders the declared profile as ffmpeg video-encoder
// arguments. The codec, preset and quality are one contract shared with the
// Python renderer and any later upscale; callers never restate encoder knobs.
func MapEncodeVideoArgs(profile MapEncodeProfile) []string {
	quality := fmt.Sprint(profile.Quality)
	if profile.ExpectGPU {
		return []string{"-c:v", profile.Codec, "-gpu", "0", "-preset", profile.Preset, "-tune", "hq", "-rc", "vbr", "-cq", quality, "-b:v", "0",
			"-profile:v", "high", "-level:v", "4.0", "-refs", "1", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-bf", "0", "-flags", "+cgop",
			"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv"}
	}
	return []string{
		"-c:v", profile.Codec, "-preset", profile.Preset, "-crf", quality,
		"-profile:v", "high", "-level:v", "4.0", "-refs", "1",
		"-color_primaries", "bt709", "-color_trc", "bt709",
		"-colorspace", "bt709", "-color_range", "tv",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-bf", "0", "-flags", "+cgop",
	}
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
