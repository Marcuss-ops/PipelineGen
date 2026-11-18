package overlays

// PhraseEntranceFrames gives a phrase a short entrance and leaves the rest of
// its certified window settled and readable. The one-third share is capped at
// 700ms so a long display window cannot turn a simple text entrance into a
// multi-second transition.
func PhraseEntranceFrames(durationUS int64, fpsNum, fpsDen int) int64 {
	if durationUS <= 0 || fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	numerator := durationUS * int64(fpsNum)
	denominator := 3_000_000 * int64(fpsDen)
	frames := (numerator + denominator - 1) / denominator
	maxFrames := (700_000*int64(fpsNum) + 1_000_000*int64(fpsDen) - 1) / (1_000_000 * int64(fpsDen))
	if frames > maxFrames {
		frames = maxFrames
	}
	return frames
}
