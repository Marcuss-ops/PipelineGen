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

// DefaultMapEncodeProfile is the production contract: GPU NVENC.
func DefaultMapEncodeProfile() MapEncodeProfile {
	return MapEncodeProfile{Codec: MapProductionCodec, ExpectGPU: true}
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
		return MapEncodeProfile{Codec: MapCPUCodec, ExpectGPU: false}, nil
	default:
		return MapEncodeProfile{}, fmt.Errorf("unsupported map encoder %q: declare %q (production) or %q (certified CPU)",
			strings.TrimSpace(explicit), MapProductionCodec, MapCPUCodec)
	}
}

// MapEncodeVideoArgs renders the profile as ffmpeg video-encoder arguments.
// The GPU branch preserves the certified production NVENC shaping exactly;
// the CPU branch is the explicit CPU policy (veryfast/CRF 23, matching the
// platform VideoConfig defaults). The codec itself always comes from the
// declared profile — callers never restate an encoder literal.
func MapEncodeVideoArgs(profile MapEncodeProfile) []string {
	if profile.ExpectGPU {
		return []string{"-c:v", profile.Codec, "-gpu", "0", "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-cq", "18", "-b:v", "0"}
	}
	return []string{"-c:v", profile.Codec, "-preset", "veryfast", "-crf", "23"}
}

// ValidateMapEncoderTelemetry certifies that the renderer's observed encoder
// matches the declared execution profile. A render produced under another
// profile is rejected with both sides named — never silently accepted.
func ValidateMapEncoderTelemetry(encoder string, gpu bool, profile MapEncodeProfile) error {
	if strings.TrimSpace(encoder) != profile.Codec || gpu != profile.ExpectGPU {
		return fmt.Errorf("Chronon map renderer used encoder=%q gpu=%t, but the declared execution profile requires encoder=%q gpu=%t",
			encoder, gpu, profile.Codec, profile.ExpectGPU)
	}
	return nil
}
