package audio

import (
	"fmt"
	"math"
)

// FrameRate is an exact rational frame rate. Common examples are 30/1 and
// 30000/1001 (29.97), avoiding floating-point timestamp drift.
type FrameRate struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// MaxRenderFrameRateDenominator is the largest denominator the Chronon canvas
// schema accepts. Standard NTSC rates (24000/1001, 30000/1001, 60000/1001)
// sit exactly at the bound; the average rate ffprobe reports for a VFR or
// remuxed source can exceed it wildly (e.g. 81920/3399 for a 24 fps clip).
const MaxRenderFrameRateDenominator int64 = 1001

// canonicalFrameRates are the standard broadcast/editing rates a noisy
// ffprobe average rate may be snapped to. Exact NTSC rationals come first so
// they win over their integer neighbour when both are within tolerance.
var canonicalFrameRates = []FrameRate{
	{Numerator: 24000, Denominator: 1001},
	{Numerator: 24, Denominator: 1},
	{Numerator: 25, Denominator: 1},
	{Numerator: 30000, Denominator: 1001},
	{Numerator: 30, Denominator: 1},
	{Numerator: 48, Denominator: 1},
	{Numerator: 50, Denominator: 1},
	{Numerator: 60000, Denominator: 1001},
	{Numerator: 60, Denominator: 1},
}

func (r FrameRate) Validate() error {
	if r.Numerator <= 0 || r.Denominator <= 0 {
		return fmt.Errorf("invalid frame rate %d/%d", r.Numerator, r.Denominator)
	}
	return nil
}

// Reduced returns the rate in lowest terms. Invalid rates are returned
// unchanged; callers that need fail-closed behaviour call Validate first.
func (r FrameRate) Reduced() FrameRate {
	if r.Numerator <= 0 || r.Denominator <= 0 {
		return r
	}
	a, b := r.Numerator, r.Denominator
	for b != 0 {
		a, b = b, a%b
	}
	return FrameRate{Numerator: r.Numerator / a, Denominator: r.Denominator / a}
}

// Normalize bounds the denominator to maxDenominator so the rate is accepted
// by consumers with a rational-frame-rate limit (Chronon caps fps_den at
// MaxRenderFrameRateDenominator). A noisy average rate is snapped to the
// CLOSEST canonical rate within 1%; if none matches, a rate already within
// the bound is reduced, and one outside it is projected onto maxDenominator
// with the closest numerator. Invalid rates are returned unchanged.
func (r FrameRate) Normalize(maxDenominator int64) FrameRate {
	if r.Validate() != nil {
		return r
	}
	if maxDenominator <= 0 {
		maxDenominator = MaxRenderFrameRateDenominator
	}
	fps := float64(r.Numerator) / float64(r.Denominator)
	best := FrameRate{}
	bestDiff := math.Inf(1)
	for _, canonical := range canonicalFrameRates {
		canonicalFPS := float64(canonical.Numerator) / float64(canonical.Denominator)
		diff := math.Abs(fps - canonicalFPS)
		if diff <= canonicalFPS*0.01 && diff < bestDiff {
			best, bestDiff = canonical, diff
		}
	}
	if bestDiff < math.Inf(1) {
		return best
	}
	if reduced := r.Reduced(); reduced.Denominator <= maxDenominator {
		return reduced
	}
	return FrameRate{
		Numerator:   int64(math.Round(fps * float64(maxDenominator))),
		Denominator: maxDenominator,
	}.Reduced()
}

func IntegerFrameRate(fps int) FrameRate {
	return FrameRate{Numerator: int64(fps), Denominator: 1}
}

// FrameResolver is the only timestamp-to-frame conversion authority.
// Rounding is nearest frame, ties rounded upward, and is applied to every
// absolute timestamp. FrameRange therefore preserves exact frame boundaries
// for contiguous destination segments.
type FrameResolver struct {
	rate FrameRate
}

func NewFrameResolver(rate FrameRate) (FrameResolver, error) {
	if err := rate.Validate(); err != nil {
		return FrameResolver{}, err
	}
	return FrameResolver{rate: rate}, nil
}

func (r FrameResolver) FrameAt(timestampUS int64) (int64, error) {
	if err := r.rate.Validate(); err != nil {
		return 0, err
	}
	if timestampUS < 0 {
		return 0, fmt.Errorf("timestamp must be non-negative: %d", timestampUS)
	}
	// timestamp_us * fps_num / (1_000_000 * fps_den), rounded half up.
	if r.rate.Denominator > math.MaxInt64/1_000_000 || timestampUS > math.MaxInt64/r.rate.Numerator {
		return 0, fmt.Errorf("timestamp %d overflows frame calculation", timestampUS)
	}
	denominator := int64(1_000_000) * r.rate.Denominator
	numerator := timestampUS * r.rate.Numerator
	if numerator > math.MaxInt64-denominator/2 {
		return 0, fmt.Errorf("timestamp %d overflows frame rounding", timestampUS)
	}
	return (numerator + denominator/2) / denominator, nil
}

func (r FrameResolver) FrameRange(startUS, durationUS int64) (startFrame, frameCount int64, err error) {
	if startUS < 0 || durationUS <= 0 || startUS > math.MaxInt64-durationUS {
		return 0, 0, fmt.Errorf("invalid frame range start_us=%d duration_us=%d", startUS, durationUS)
	}
	startFrame, err = r.FrameAt(startUS)
	if err != nil {
		return 0, 0, err
	}
	endFrame, err := r.FrameAt(startUS + durationUS)
	if err != nil {
		return 0, 0, err
	}
	if endFrame <= startFrame {
		return 0, 0, fmt.Errorf("frame range rounds to zero frames: start_us=%d duration_us=%d", startUS, durationUS)
	}
	return startFrame, endFrame - startFrame, nil
}

// FrameCountForDuration returns the deterministic nominal count for a
// duration independent of its absolute placement. It is useful when a
// caller must explicitly preserve a source/destination frame-count invariant.
func (r FrameResolver) FrameCountForDuration(durationUS int64) (int64, error) {
	if durationUS <= 0 {
		return 0, fmt.Errorf("duration must be positive: %d", durationUS)
	}
	count, err := r.FrameAt(durationUS)
	if err != nil {
		return 0, err
	}
	if count <= 0 {
		return 0, fmt.Errorf("duration rounds to zero frames: %d", durationUS)
	}
	return count, nil
}

// FrameAlignedDurationUS returns the smallest duration in microseconds that is
// a whole number of frames and at least durationUS. The video renderer emits
// FrameAt(durationUS) frames, which rounds the canonical (microsecond)
// timeline up or down to a frame boundary; the audio master pads to this value
// so final_audio always covers the frame-aligned video (audio_duration >=
// video_duration). A 17.52s narration at 30fps rounds to 526 frames = 17.533s,
// which is the scene-8 freeze-tail case this guarantees against.
func (r FrameResolver) FrameAlignedDurationUS(durationUS int64) (int64, error) {
	if err := r.rate.Validate(); err != nil {
		return 0, err
	}
	if durationUS < 0 {
		return 0, fmt.Errorf("duration must be non-negative: %d", durationUS)
	}
	if durationUS == 0 {
		return 0, nil
	}
	frames, err := r.FrameAt(durationUS)
	if err != nil {
		return 0, err
	}
	if frames <= 0 {
		return 0, fmt.Errorf("duration rounds to zero frames: %d", durationUS)
	}
	// frames * frame_duration_us, floored. For integer rates this is exact;
	// rational rates (e.g. 30000/1001) floor to the nearest microsecond.
	if frames > math.MaxInt64/(1_000_000*r.rate.Denominator) {
		return 0, fmt.Errorf("duration %d overflows frame-aligned microseconds", durationUS)
	}
	aligned := frames * 1_000_000 * r.rate.Denominator / r.rate.Numerator
	if aligned < durationUS {
		aligned = durationUS
	}
	return aligned, nil
}
