package media

// frame_time.go owns the ONE frame↔microsecond conversion for the assembly
// grid. The same formula existed in three places (final-job source range,
// final-job prepare and the wiring placement) with the FPS as a literal in one
// of them; a drift there silently shifts a segment by a frame.

// FrameIndexToUSAtFPS converts a frame index to microseconds on the given
// rational grid, rounding to the nearest microsecond. A non-positive grid is
// reported as 0 rather than dividing by zero.
func FrameIndexToUSAtFPS(frame, fpsNum, fpsDen int64) int64 {
	if fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return (frame*1_000_000*fpsDen + fpsNum/2) / fpsNum
}

// FrameDurationUSAtFPS converts a frame COUNT to microseconds on the given
// rational grid. A non-positive count has no duration.
func FrameDurationUSAtFPS(frameCount, fpsNum, fpsDen int64) int64 {
	if frameCount <= 0 {
		return 0
	}
	return FrameIndexToUSAtFPS(frameCount, fpsNum, fpsDen)
}

// FrameDurationUS is FrameDurationUSAtFPS on the canonical assembly grid.
func FrameDurationUS(frameCount int64) int64 {
	return FrameDurationUSAtFPS(frameCount, CanonicalFPSNum, CanonicalFPSDen)
}
