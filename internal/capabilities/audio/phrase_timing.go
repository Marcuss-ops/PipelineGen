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
// Timestamps come exclusively from LocatePhrase — the first matched word's
// start to the last matched word's end — never interpolated and never
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
// matched word's end via LocatePhrase — never interpolated or invented.
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

// LocateSceneSpeechTiming projects one scene's canonical word timing onto the
// final timeline as a SceneSpeechTiming. It reuses LocatePhraseTimings (hence
// LocatePhrase) for the phrase spans and copies the word boundaries verbatim;
// no timestamp is ever interpolated or invented. It is fail-closed: an invalid
// timing, a negative timeline offset, an empty scene id, or a phrase that does
// not occur verbatim aborts the whole projection.
func LocateSceneSpeechTiming(sceneIndex int, sceneID, voiceoverAssetID string, timelineStartUS int64, timing SpeechTimingArtifact, phrases []string) (SceneSpeechTiming, error) {
	if strings.TrimSpace(sceneID) == "" {
		return SceneSpeechTiming{}, fmt.Errorf("%w: empty scene id", ErrInvalidSceneSpeechTiming)
	}
	located, err := LocatePhraseTimings(sceneIndex, timelineStartUS, timing, phrases)
	if err != nil {
		return SceneSpeechTiming{}, err
	}
	words := make([]SpeechWordTiming, len(timing.Words))
	copy(words, timing.Words)
	out := SceneSpeechTiming{
		SceneID:          sceneID,
		VoiceoverAssetID: voiceoverAssetID,
		LocalDurationUS:  timing.DurationUS,
		Words:            words,
		Phrases:          located,
	}
	if err := out.Validate(); err != nil {
		return SceneSpeechTiming{}, err
	}
	return out, nil
}

// LocatePhraseTimings projects each phrase onto the scene's canonical word
// timing and maps the local span onto the final timeline via timelineStartUS.
// It is deterministic and fail-closed:
//
//   - an invalid timing artifact returns an error (never timestamps);
//   - a missing phrase returns an error wrapping ErrPhraseNotFound — a phrase
//     that does not occur verbatim in the speech is never fabricated;
//   - a negative scene index or timeline offset is rejected.
//
// Output order matches input phrase order; PhraseIndex is the position in the
// input list and WordStart/WordEnd reference the artifact word indices. Each
// phrase uses its first document-order occurrence.
func LocatePhraseTimings(sceneIndex int, timelineStartUS int64, timing SpeechTimingArtifact, phrases []string) ([]PhraseTiming, error) {
	if err := timing.Validate(); err != nil {
		return nil, err
	}
	if sceneIndex < 0 {
		return nil, fmt.Errorf("%w: negative scene index %d", ErrInvalidPhraseTiming, sceneIndex)
	}
	if timelineStartUS < 0 {
		return nil, fmt.Errorf("%w: negative timeline start %d", ErrInvalidPhraseTiming, timelineStartUS)
	}

	out := make([]PhraseTiming, 0, len(phrases))
	for i, phrase := range phrases {
		located, err := LocatePhrase(timing, phrase)
		if err != nil {
			return nil, fmt.Errorf("scene %d phrase %d: %w", sceneIndex, i, err)
		}
		first := located[0]
		out = append(out, PhraseTiming{
			SceneIndex:      sceneIndex,
			PhraseIndex:     i,
			Text:            first.Text,
			WordStart:       first.WordStart,
			WordEnd:         first.WordEnd,
			LocalStartUS:    first.StartUS,
			LocalEndUS:      first.EndUS,
			TimelineStartUS: timelineStartUS,
			GlobalStartUS:   timelineStartUS + first.StartUS,
			GlobalEndUS:     timelineStartUS + first.EndUS,
		})
	}
	return out, nil
}
