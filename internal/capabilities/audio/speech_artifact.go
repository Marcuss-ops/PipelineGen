package audio

import (
	"fmt"
)

// BuildSpeechTimingArtifact assembles and validates the canonical,
// provider-neutral SpeechTimingArtifact from already-collected inputs.
// It is PURE: the caller computes textSHA256 (the exact synthesized
// text) and audioSHA256 (the final audio file bytes) at the I/O
// boundary; this builder never touches files.
//
// The hashes bind the artifact to exactly one synthesized text and one
// audio file, so downstream code can prove "this timing belongs to
// THIS mp3". Validation is fail-closed: non-contiguous indices,
// non-monotonic boundaries, negative timestamps or words past the
// audio duration produce a typed validation error instead of a
// plausible-but-wrong artifact.
func BuildSpeechTimingArtifact(
	provider string,
	language string,
	voice string,
	textSHA256 string,
	audioSHA256 string,
	durationUS int64,
	words []SpeechWordTiming,
) (*SpeechTimingArtifact, error) {
	if textSHA256 == "" {
		return nil, fmt.Errorf("speech timing artifact: text_sha256 is required (binds the artifact to the synthesized text)")
	}
	if audioSHA256 == "" {
		return nil, fmt.Errorf("speech timing artifact: audio_sha256 is required (binds the artifact to the final audio)")
	}
	artifact := &SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     provider,
		BoundaryMode: BoundaryWord,
		Language:     language,
		Voice:        voice,
		TextSHA256:   textSHA256,
		AudioSHA256:  audioSHA256,
		DurationUS:   durationUS,
		Words:        append([]SpeechWordTiming(nil), words...),
	}
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("speech timing artifact: %w", err)
	}
	return artifact, nil
}
