package audio

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validSpeechTimingArtifact() SpeechTimingArtifact {
	return SpeechTimingArtifact{
		Version:      SpeechTimingVersion,
		Provider:     "edge_tts",
		BoundaryMode: BoundaryWord,
		Language:     "it",
		Voice:        "it-IT-DiegoNeural",
		TextSHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AudioSHA256:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		DurationUS:   1_834_200_000,
		Words: []SpeechWordTiming{
			{Index: 0, Text: "Il", StartUS: 0, EndUS: 125_000},
			{Index: 1, Text: "celebre", StartUS: 125_000, EndUS: 487_000},
			{Index: 2, Text: "incontro", StartUS: 487_000, EndUS: 1_020_000},
		},
	}
}

func TestSpeechTiming_ValidateValid(t *testing.T) {
	if err := validSpeechTimingArtifact().Validate(); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
}

func TestSpeechTiming_ValidateRejectsInvalidVersion(t *testing.T) {
	for _, version := range []int{0, -1} {
		artifact := validSpeechTimingArtifact()
		artifact.Version = version
		if err := artifact.Validate(); !errors.Is(err, ErrInvalidTimingVersion) {
			t.Fatalf("version %d error = %v, want ErrInvalidTimingVersion", version, err)
		}
	}
}

func TestSpeechTiming_RejectUnsupportedBoundaryMode(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	artifact.BoundaryMode = "sentence"
	if err := artifact.Validate(); !errors.Is(err, ErrUnsupportedBoundaryMode) {
		t.Fatalf("error = %v, want ErrUnsupportedBoundaryMode", err)
	}
}

func TestSpeechTiming_RejectInvalidWordIndex(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	artifact.Words[1].Index = 3
	if err := artifact.Validate(); !errors.Is(err, ErrInvalidWordIndex) {
		t.Fatalf("error = %v, want ErrInvalidWordIndex", err)
	}
}

func TestSpeechTiming_RejectNegativeStart(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	artifact.Words[1].StartUS = -1
	if err := artifact.Validate(); !errors.Is(err, ErrNegativeTiming) {
		t.Fatalf("error = %v, want ErrNegativeTiming", err)
	}
}

func TestSpeechTiming_RejectEndBeforeStart(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	artifact.Words[1].EndUS = artifact.Words[1].StartUS - 1
	if err := artifact.Validate(); !errors.Is(err, ErrInvalidTimingRange) {
		t.Fatalf("error = %v, want ErrInvalidTimingRange", err)
	}
}

func TestSpeechTiming_RejectNonMonotonic(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	// Word 2 starts before word 1 ends.
	artifact.Words[2].StartUS = artifact.Words[1].EndUS - 1
	if err := artifact.Validate(); !errors.Is(err, ErrNonMonotonicTiming) {
		t.Fatalf("error = %v, want ErrNonMonotonicTiming", err)
	}
}

func TestSpeechTiming_RejectWordPastDuration(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	artifact.DurationUS = artifact.Words[2].EndUS - 1
	if err := artifact.Validate(); !errors.Is(err, ErrTimingPastAudioDuration) {
		t.Fatalf("error = %v, want ErrTimingPastAudioDuration", err)
	}
}

// TestSpeechTiming_AllWordsWithinFinalAudioDuration pins the containment
// invariant: every word boundary must sit inside the FINAL audio duration
// (the post-silence-remap duration stored in duration_us). The last word may
// end exactly AT the duration (inclusive boundary), but never past it.
func TestSpeechTiming_AllWordsWithinFinalAudioDuration(t *testing.T) {
	artifact := validSpeechTimingArtifact()
	last := len(artifact.Words) - 1

	// Final audio duration == the last word's end: the boundary is inclusive.
	artifact.DurationUS = artifact.Words[last].EndUS
	if err := artifact.Validate(); err != nil {
		t.Fatalf("artifact with last word ending exactly at duration rejected: %v", err)
	}

	// Every word must be strictly contained within the final duration.
	for i, w := range artifact.Words {
		if w.StartUS < 0 || w.EndUS > artifact.DurationUS {
			t.Fatalf("word %d [%d,%d) is not within final duration %d", i, w.StartUS, w.EndUS, artifact.DurationUS)
		}
	}

	// The negative half of the same invariant: one word past the final
	// duration fails closed.
	artifact.DurationUS = artifact.Words[last].EndUS - 1
	if err := artifact.Validate(); !errors.Is(err, ErrTimingPastAudioDuration) {
		t.Fatalf("word past final duration error = %v, want ErrTimingPastAudioDuration", err)
	}
}

func TestSpeechTiming_JSONRoundTrip(t *testing.T) {
	original := validSpeechTimingArtifact()
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "provider", "boundary_mode", "language", "voice", "text_sha256", "audio_sha256", "duration_us", "words"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("artifact missing canonical JSON field %q: %s", key, encoded)
		}
	}
	var decoded SpeechTimingArtifact
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", decoded, original)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded artifact invalid: %v", err)
	}
}

func TestSpeechTiming_DeepCopy(t *testing.T) {
	original := validSpeechTimingArtifact()
	copy := original.DeepCopy()
	if &copy.Words[0] == &original.Words[0] {
		t.Fatal("deep copy shares the Words backing array")
	}
	copy.Words[0].StartUS = 999_999
	if original.Words[0].StartUS == 999_999 {
		t.Fatal("mutating the copy changed the original")
	}
	copy.Words = append(copy.Words, SpeechWordTiming{Index: 3, Text: "extra", StartUS: 2_000_000, EndUS: 2_500_000})
	if len(original.Words) != 3 {
		t.Fatalf("appending to the copy changed the original length: %d", len(original.Words))
	}
}
