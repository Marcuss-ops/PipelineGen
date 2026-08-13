package audio

import (
	"errors"
	"fmt"
)

// SpeechTimingVersion is the schema version for SpeechTimingArtifact files.
// Bump it whenever the canonical timing JSON shape or semantics change so
// cached artifacts cannot be silently misread.
const SpeechTimingVersion = 1

// BoundaryMode identifies the granularity of speech boundaries captured by a
// TTS provider. Only word-level boundaries are supported; anything else is
// rejected by Validate rather than approximated downstream.
type BoundaryMode string

const (
	BoundaryWord BoundaryMode = "word"
)

// SpeechWordTiming is one word-level boundary. All values are integer
// microseconds, never floats, so downstream consumers never accumulate
// rounding errors across projections (timeline, SRT, VTT, phrase lookup).
type SpeechWordTiming struct {
	Index   int    `json:"index"`
	Text    string `json:"text"`
	StartUS int64  `json:"start_us"`
	EndUS   int64  `json:"end_us"`
}

// SpeechTimingArtifact is the canonical, provider-neutral SSOT for synthesized
// voiceover timing. Providers hand over raw boundaries; the application layer
// is responsible for building this artifact once the final audio exists.
//
// The hashes bind the artifact to exactly one synthesized text and one audio
// file: text_sha256 covers the exact text sent to synthesis and audio_sha256
// covers the final published audio bytes.
type SpeechTimingArtifact struct {
	Version      int          `json:"version"`
	Provider     string       `json:"provider"`
	BoundaryMode BoundaryMode `json:"boundary_mode"`
	Language     string       `json:"language"`
	Voice        string       `json:"voice,omitempty"`

	TextSHA256  string `json:"text_sha256"`
	AudioSHA256 string `json:"audio_sha256"`

	DurationUS int64              `json:"duration_us"`
	Words      []SpeechWordTiming `json:"words"`
}

var (
	ErrInvalidTimingVersion    = errors.New("invalid speech timing version")
	ErrUnsupportedBoundaryMode = errors.New("unsupported speech boundary mode")
	ErrInvalidWordIndex        = errors.New("invalid speech word index")
	ErrNegativeTiming          = errors.New("negative speech timing")
	ErrInvalidTimingRange      = errors.New("speech word end before start")
	ErrNonMonotonicTiming      = errors.New("speech words out of order")
	ErrTimingPastAudioDuration = errors.New("speech word extends past audio duration")
)

// Validate enforces the full canonical timing contract: schema version,
// supported boundary mode, contiguous word indices, non-negative monotonic
// microsecond ranges, and containment within the audio duration.
func (a SpeechTimingArtifact) Validate() error {
	if a.Version <= 0 {
		return ErrInvalidTimingVersion
	}
	if a.BoundaryMode != BoundaryWord {
		return fmt.Errorf("%w: %q", ErrUnsupportedBoundaryMode, a.BoundaryMode)
	}
	var previousEnd int64
	for i, word := range a.Words {
		if word.Index != i {
			return fmt.Errorf("%w: got %d at position %d", ErrInvalidWordIndex, word.Index, i)
		}
		if word.StartUS < 0 {
			return fmt.Errorf("%w: word %d start %d", ErrNegativeTiming, i, word.StartUS)
		}
		if word.EndUS < word.StartUS {
			return fmt.Errorf("%w: word %d [%d, %d)", ErrInvalidTimingRange, i, word.StartUS, word.EndUS)
		}
		if i > 0 && word.StartUS < previousEnd {
			return fmt.Errorf("%w: word %d starts at %d before previous end %d", ErrNonMonotonicTiming, i, word.StartUS, previousEnd)
		}
		if word.EndUS > a.DurationUS {
			return fmt.Errorf("%w: word %d ends at %d past duration %d", ErrTimingPastAudioDuration, i, word.EndUS, a.DurationUS)
		}
		previousEnd = word.EndUS
	}
	return nil
}

// DeepCopy returns a fully independent copy so callers (clone, merge, cache)
// can mutate a copy without aliasing the original Words slice.
func (a SpeechTimingArtifact) DeepCopy() SpeechTimingArtifact {
	clone := a
	if a.Words != nil {
		clone.Words = make([]SpeechWordTiming, len(a.Words))
		copy(clone.Words, a.Words)
	}
	return clone
}

// AudioFileHasher is the narrow port for computing the SHA-256 digest of the
// final audio file. Concrete adapters live in the platform layer; the
// application layer must never open files directly (I/O binder gate).
type AudioFileHasher interface {
	SHA256File(path string) (string, error)
}
