package voiceover

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// BuildTimingArtifact builds the canonical, provider-neutral timing
// artifact from RAW provider boundaries once the FINAL audio exists.
//
// The provider only ever hands over raw word boundaries; this function
// is the single application-layer site that turns them into the
// SpeechTimingArtifact SSOT. The hashes bind the artifact to exactly
// one synthesized text (the exact bytes sent to TTS) and one audio
// file (the final bytes, post silence-removal), so downstream code can
// prove "this timing belongs to THIS mp3".
//
// The result is validated fail-closed: non-monotonic boundaries,
// negative timestamps or words past the audio duration produce a typed
// validation error instead of a plausible-but-wrong artifact.
func BuildTimingArtifact(
	text string,
	provider string,
	language string,
	voice string,
	finalAudioPath string,
	durationUS int64,
	boundaries []RawSpeechBoundary,
) (*audio.SpeechTimingArtifact, error) {
	if finalAudioPath == "" {
		return nil, fmt.Errorf("timing artifact: final audio path is required")
	}
	textHash := sha256Hex(text)
	audioHash, err := sha256File(finalAudioPath)
	if err != nil {
		return nil, err
	}

	words := make([]audio.SpeechWordTiming, len(boundaries))
	for i, b := range boundaries {
		words[i] = audio.SpeechWordTiming{
			Index:   i,
			Text:    b.Text,
			StartUS: b.StartUS,
			EndUS:   b.EndUS,
		}
	}

	artifact := &audio.SpeechTimingArtifact{
		Version:      audio.SpeechTimingVersion,
		Provider:     provider,
		BoundaryMode: audio.BoundaryWord,
		Language:     language,
		Voice:        voice,
		TextSHA256:   textHash,
		AudioSHA256:  audioHash,
		DurationUS:   durationUS,
		Words:        words,
	}
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("timing artifact: %w", err)
	}
	return artifact, nil
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func sha256File(path string) (string, error) {
	// The canonical infra files helper (also used by
	// internal/application/assets/verification/verified.go) so the
	// application layer never opens files directly (iobinder gate).
	return hashutil.HashFile(path, sha256.New())
}
