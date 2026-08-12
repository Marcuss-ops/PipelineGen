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

func (r FrameRate) Validate() error {
	if r.Numerator <= 0 || r.Denominator <= 0 {
		return fmt.Errorf("invalid frame rate %d/%d", r.Numerator, r.Denominator)
	}
	return nil
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

func (r FrameResolver) Rate() FrameRate { return r.rate }

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
