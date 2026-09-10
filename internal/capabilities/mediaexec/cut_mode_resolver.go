// Package mediaexec — cut_mode_resolver.go: the SOLE canonical owner of the
// copy-vs-render decision for segment cutting.
//
// Speed audit (Sept 2026): the YouTube ingest path used to run TWO FFmpeg
// operations per segment when the full source was pre-downloaded
// (CutCopy into a temp file, then CutAndNormalize over it). Every consumer
// (YouTube ingest, stock, reprocess) now asks THIS resolver exactly once per
// segment:
//
//	CutModeCopy      -> stream-copy straight to the final artifact (fast, no encode)
//	CutModeNormalize -> ONE render from the source (never a copy-then-render chain)
//
// The resolver never looks at concrete encoders or file paths; it only
// compares immutable source facts against the canonical target profile.
package mediaexec

import "strings"

// MediaFacts is the immutable, capability-neutral description of a source
// media file that cut-eligibility decisions need. It is produced ONCE per
// extraction (probe of the staged full source) and never mutated afterwards.
type MediaFacts struct {
	VideoCodec  string
	PixelFormat string
	Width       int
	Height      int
	FPSNum      int
	FPSDen      int
	HasAudio    bool
	AudioCodec  string
	SampleRate  int
	Channels    int
}

// CutMode is the single decision the resolver produces: copy the stream or
// render it to the canonical profile.
type CutMode string

const (
	// CutModeCopy stream-copies the source interval with no re-encode.
	// Only legal when the source is already canonical (same codec/pixel
	// format/dimensions/fps/audio) AND the segment boundary is
	// stream-copy safe.
	CutModeCopy CutMode = "copy"
	// CutModeNormalize renders the interval to the canonical profile
	// exactly once (encoder policy delegated to the execution plane).
	CutModeNormalize CutMode = "normalize"
)

// CutEligibilityInput is the complete, immutable input to a cut-mode
// decision. All fields must be resolved before the call — the resolver
// performs no I/O.
type CutEligibilityInput struct {
	// Source is the probed facts of the FULL source file (not of the
	// segment). Copy eligibility must compare the source stream against
	// the target profile; a segment's facts are only known after a render.
	Source MediaFacts
	// Target is the canonical profile the final artifact must conform to.
	// Zero values fall back to the frozen assembly contract defaults.
	Target VideoProfile
	// StartSec / EndSec bound the requested segment inside the source.
	StartSec float64
	EndSec   float64
	// KeepAudio mirrors the caller's audio-preservation intent; when
	// false the audio stream is dropped and only video conformance is
	// checked.
	KeepAudio bool
	// StreamCopySafe reports whether the requested interval can be
	// stream-copied without timestamp/keyframe drift that the caller is
	// unwilling to accept. The YouTube ingest path treats an aligned
	// boundary as safe; conservative callers (exact-frame stock cuts)
	// pass false to force CutModeNormalize.
	StreamCopySafe bool
}

// CutModeResolver is the canonical decision port. Implementations must be
// pure (no I/O, no logging); the default implementation is the SSOT.
type CutModeResolver interface {
	ResolveCutMode(CutEligibilityInput) CutMode
}

// DefaultCutModeResolver is the canonical copy-eligibility policy.
//
// Copy eligibility requires ALL of:
//   - video codec == h264 (the canonical ingest codec family)
//   - pixel format == yuv420p (8-bit 4:2:0 — required for downstream muxing)
//   - width/height == target exactly
//   - fps == target exact rational
//   - segment boundary stream-copy safe
//   - when audio is kept: source HAS audio, codec == aac, sample rate and
//     channels == target (when the target declares them)
//
// The ingest path never applies visual effects or watermarks to source
// clips (that composition belongs to RenderingGen/Chronon), so no effect
// input is modelled here.
type DefaultCutModeResolver struct{}

// ResolveCutMode implements CutModeResolver.
func (DefaultCutModeResolver) ResolveCutMode(in CutEligibilityInput) CutMode {
	target := in.Target.WithDefaults()
	f := in.Source

	videoConformant := strings.EqualFold(strings.TrimSpace(f.VideoCodec), "h264") &&
		strings.EqualFold(strings.TrimSpace(f.PixelFormat), "yuv420p") &&
		f.Width == target.Width &&
		f.Height == target.Height &&
		f.FPSNum == target.FPSNum &&
		f.FPSDen == target.FPSDen

	if !videoConformant || !in.StreamCopySafe {
		return CutModeNormalize
	}

	if in.KeepAudio {
		if !f.HasAudio ||
			!strings.EqualFold(strings.TrimSpace(f.AudioCodec), "aac") ||
			(target.SampleRate > 0 && f.SampleRate != target.SampleRate) ||
			(target.Channels > 0 && f.Channels != target.Channels) {
			return CutModeNormalize
		}
	}

	return CutModeCopy
}

// ResolveCutMode is the package-level helper for callers that do not need
// to inject a custom resolver. It is the SSOT decision entry point.
func ResolveCutMode(in CutEligibilityInput) CutMode {
	return (DefaultCutModeResolver{}).ResolveCutMode(in)
}