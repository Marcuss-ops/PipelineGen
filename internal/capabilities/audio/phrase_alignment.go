package audio

import (
	"context"
	"errors"
)

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
