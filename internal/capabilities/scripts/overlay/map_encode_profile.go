package overlay

import (
	"fmt"
	"strings"
)

// MapEncodeProfile is the DECLARED execution profile for a map render: which
// video codec the artifact must carry and whether a GPU encoder is expected.
//
// Cartographic validity and encoder choice are separate concerns: the map
// renderer may run on GPU (production) or CPU (explicitly certified), but the
// observed telemetry must match the declared profile. A mismatch fails
// closed — the boundary never silently substitutes one codec for another, so
// existing NVENC jobs keep their exact output contract and a CPU render is
// only ever produced under an explicit operator opt-in.
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
	// MapProductionCodec is the certified production map encoder.
	MapProductionCodec = "h264_nvenc"
	// MapCPUCodec is the separately certified CPU map encoder. It is never
	// selected implicitly: only an explicit operator declaration opts in.
	MapCPUCodec = "libx264"
	// MapEncodeProfileEnv names the explicit operator opt-in. Empty means the
	// production default; naming the CPU codec certifies the CPU path for
	// this render. Anything else fails closed.
	MapEncodeProfileEnv = "PIPELINEGEN_MAP_CODEC"
)

// DefaultMapEncodeProfile is the production contract: GPU NVENC at preset p6/CQ 15.
func DefaultMapEncodeProfile() MapEncodeProfile {
	return MapEncodeProfile{Codec: MapProductionCodec, ExpectGPU: true, Preset: "p6", Quality: 15}
}

// ResolveMapEncodeProfile declares the execution profile for one map render.
// Empty selects the production default; the CPU codec selects the separately
// certified CPU path. An unknown name fails closed instead of degrading to
// whichever encoder happens to be available.
func ResolveMapEncodeProfile(explicit string) (MapEncodeProfile, error) {
	switch strings.TrimSpace(strings.ToLower(explicit)) {
	case "":
		return DefaultMapEncodeProfile(), nil
	case MapProductionCodec:
		return DefaultMapEncodeProfile(), nil
	case MapCPUCodec:
		return MapEncodeProfile{Codec: MapCPUCodec, ExpectGPU: false, Preset: "veryfast", Quality: 23}, nil
	default:
		return MapEncodeProfile{}, fmt.Errorf("unsupported map encoder %q: declare %q (production) or %q (certified CPU)",
			strings.TrimSpace(explicit), MapProductionCodec, MapCPUCodec)
	}
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
