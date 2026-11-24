package audio

import (
	"context"
	"errors"
	"fmt"
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

// LocatedPhrase is one deterministic match of a phrase inside the canonical
// word-level timing. WordStart/WordEnd reference the artifact word indices;
// StartUS/EndUS span from the first matched word's start to the last matched
// word's end, so the caller gets exact microsecond anchors without any
// invented interpolation.
//
// The Rust semantic engine produces this value. Go only validates the returned
// word boundaries and projects them; it never matches words itself.
type LocatedPhrase struct {
	Text       string `json:"text"`
	Occurrence int    `json:"occurrence"`

	WordStart int `json:"word_start"`
	WordEnd   int `json:"word_end"`

	StartUS int64 `json:"start_us"`
	EndUS   int64 `json:"end_us"`
}

// ErrPhraseNotFound is returned when the phrase does not occur in the
// artifact as an exact contiguous token match. No fuzzy matching is ever
// attempted: a missing phrase fails explicitly rather than producing a
// plausible-but-wrong timestamp.
var ErrPhraseNotFound = errors.New("phrase not found in speech timing")

// PhraseAlignmentRequest asks the Rust worker to align phrases against one
// certified word-timing artifact.
type PhraseAlignmentRequest struct {
	Timing  SpeechTimingArtifact
	Phrases []string

	// AllowMissing skips values the speech never said instead of failing the
	// whole batch. It is for optional entity/moment annotations; phrase timing
	// keeps the fail-closed default.
	AllowMissing bool
	// AllOccurrences returns every document-order match. Moment grounding
	// renders repetitions; phrase timing uses only the first match.
	AllOccurrences bool
}

// PhraseAligner is the single production seam for phrase/word alignment.
// Implementations must fail closed when the worker is not configured.
type PhraseAligner interface {
	AlignPhrases(ctx context.Context, request PhraseAlignmentRequest) ([]LocatedPhrase, error)
}
