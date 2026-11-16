package audio

import (
	"errors"
	"fmt"
	"strings"
)

// PhraseTiming is a read-only projection of one script phrase onto the
// canonical word timing. It carries two time coordinates:
//
//   - LocalStartUS/LocalEndUS reference the scene's own voiceover audio
//     (the per-scene MP3/M4A fragment before it is mixed into the master).
//   - GlobalStartUS/GlobalEndUS reference the final combined timeline,
//     computed as TimelineStartUS (the scene's canonical timeline offset,
//     CanonicalTimeline.Segments[SceneIndex].TimelineStartUS) plus the
//     local span.
//
// Timestamps come exclusively from the Rust aligner — the first matched
// word's start to the last matched word's end — never interpolated and never
// invented by a model.
type PhraseTiming struct {
	SceneIndex  int `json:"scene_index"`
	PhraseIndex int `json:"phrase_index"`

	Text string `json:"text"`

	WordStart int `json:"word_start"`
	WordEnd   int `json:"word_end"`

	LocalStartUS int64 `json:"local_start_us"`
	LocalEndUS   int64 `json:"local_end_us"`

	TimelineStartUS int64 `json:"timeline_start_us"`

	GlobalStartUS int64 `json:"global_start_us"`
	GlobalEndUS   int64 `json:"global_end_us"`
}

// ErrInvalidPhraseTiming is returned when a PhraseTiming projection violates
// its invariants (negative indices, inverted spans, or a global span that is
// not the canonical timeline offset plus the local span).
var ErrInvalidPhraseTiming = errors.New("invalid phrase timing")

// Validate enforces the projection invariants so a consumer can never trust a
// timestamp that drifted from the canonical local→global mapping.
func (p PhraseTiming) Validate() error {
	if p.SceneIndex < 0 || p.PhraseIndex < 0 {
		return fmt.Errorf("%w: negative index (scene %d, phrase %d)", ErrInvalidPhraseTiming, p.SceneIndex, p.PhraseIndex)
	}
	if strings.TrimSpace(p.Text) == "" {
		return fmt.Errorf("%w: empty phrase text", ErrInvalidPhraseTiming)
	}
	if p.WordEnd < p.WordStart {
		return fmt.Errorf("%w: word span [%d,%d]", ErrInvalidPhraseTiming, p.WordStart, p.WordEnd)
	}
	if p.LocalEndUS < p.LocalStartUS {
		return fmt.Errorf("%w: inverted local span [%d,%d)", ErrInvalidPhraseTiming, p.LocalStartUS, p.LocalEndUS)
	}
	if p.TimelineStartUS < 0 {
		return fmt.Errorf("%w: negative timeline start %d", ErrInvalidPhraseTiming, p.TimelineStartUS)
	}
	if p.GlobalStartUS != p.TimelineStartUS+p.LocalStartUS {
		return fmt.Errorf("%w: global start %d != timeline %d + local %d", ErrInvalidPhraseTiming, p.GlobalStartUS, p.TimelineStartUS, p.LocalStartUS)
	}
	if p.GlobalEndUS != p.TimelineStartUS+p.LocalEndUS {
		return fmt.Errorf("%w: global end %d != timeline %d + local %d", ErrInvalidPhraseTiming, p.GlobalEndUS, p.TimelineStartUS, p.LocalEndUS)
	}
	return nil
}

// SceneSpeechTiming is the scene-level projection of the canonical
// SpeechTimingArtifact onto the final combined timeline. It bundles the
// scene's word-level boundaries (Words) with the derived phrase→timestamp
// spans (Phrases), in two coordinates:
//
//   - local: the scene's own voiceover audio (VoiceoverAssetID), whose
//     certified duration is LocalDurationUS;
//   - global: the final master timeline, where each phrase global span is
//     the scene's canonical timeline offset plus the local span.
//
// It is a read-only projection of the canonical SpeechTimingArtifact files:
// every timestamp comes from the first matched word's start to the last
// matched word's end via the Rust aligner — never interpolated or invented.
type SceneSpeechTiming struct {
	SceneID          string             `json:"scene_id"`
	VoiceoverAssetID string             `json:"voiceover_asset_id"`
	LocalDurationUS  int64              `json:"local_duration_us"`
	Words            []SpeechWordTiming `json:"words"`
	Phrases          []PhraseTiming     `json:"phrases"`
}

// ErrInvalidSceneSpeechTiming is returned when a SceneSpeechTiming projection
// violates its invariants (missing scene id, invalid word boundaries, or a
// phrase whose global span is not the canonical timeline offset plus the
// local span).
var ErrInvalidSceneSpeechTiming = errors.New("invalid scene speech timing")

// Validate enforces the scene-level projection invariants. Word boundaries
// must be contiguous, non-negative, monotonic and contained in the local
// duration; every phrase must independently satisfy the local→global mapping.
func (s SceneSpeechTiming) Validate() error {
	if strings.TrimSpace(s.SceneID) == "" {
		return fmt.Errorf("%w: empty scene id", ErrInvalidSceneSpeechTiming)
	}
	if s.LocalDurationUS < 0 {
		return fmt.Errorf("%w: negative local duration %d", ErrInvalidSceneSpeechTiming, s.LocalDurationUS)
	}
	var previousEnd int64
	for i, word := range s.Words {
		if word.Index != i {
			return fmt.Errorf("%w: word %d has index %d", ErrInvalidSceneSpeechTiming, i, word.Index)
		}
		if word.StartUS < 0 || word.EndUS < word.StartUS {
			return fmt.Errorf("%w: word %d range [%d,%d)", ErrInvalidSceneSpeechTiming, i, word.StartUS, word.EndUS)
		}
		if i > 0 && word.StartUS < previousEnd {
			return fmt.Errorf("%w: word %d starts before previous end", ErrInvalidSceneSpeechTiming, i)
		}
		if word.EndUS > s.LocalDurationUS {
			return fmt.Errorf("%w: word %d ends at %d past local duration %d", ErrInvalidSceneSpeechTiming, i, word.EndUS, s.LocalDurationUS)
		}
		previousEnd = word.EndUS
	}
	for _, phrase := range s.Phrases {
		if err := phrase.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSceneSpeechTiming, err)
		}
	}
	return nil
}
