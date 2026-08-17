package entities

import (
	"errors"
	"fmt"
	"strings"
)

// EntityTimelineVersion is the schema version of the canonical
// entity-timeline projection. Bump it whenever the JSON shape or semantics
// change so cached artifacts cannot be silently misread.
const EntityTimelineVersion = 1

// EntityOccurrence is one grounded occurrence of an entity in a scene. All
// timestamps are integer microseconds, never floats, so downstream consumers
// never accumulate rounding errors across projections (overlays, SRT, VTT).
//
// Two time coordinates are carried, mirroring audio.PhraseTiming:
//
//   - LocalStartUS/LocalEndUS reference the scene's own voiceover audio (the
//     per-scene fragment before it is mixed into the master), derived from
//     the first matched word's start to the last matched word's end in the
//     canonical word timing.
//   - AudioStartUS/AudioEndUS reference the final combined timeline (the
//     "global" span the overlays consume): TimelineStartUS (the scene's
//     canonical timeline offset) plus the local span.
//
// The projection invariant is AudioStartUS == TimelineStartUS+LocalStartUS
// (and the same for the end); Validate rejects any occurrence that drifted.
type EntityOccurrence struct {
	EntityID   string `json:"entity_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	SceneID    string `json:"scene_id"`
	SceneIndex int    `json:"scene_index"`
	// TextStart/TextEnd are Unicode-rune offsets of the entity's first
	// verbatim mention in the scene text (never UTF-8 byte offsets).
	TextStart int `json:"text_start"`
	TextEnd   int `json:"text_end"`
	// WordStart/WordEnd are the first/last matched word indices in the
	// canonical word timing.
	WordStart int `json:"word_start"`
	WordEnd   int `json:"word_end"`

	LocalStartUS int64 `json:"local_start_us"`
	LocalEndUS   int64 `json:"local_end_us"`
	// TimelineStartUS is the scene's canonical timeline offset (its absolute
	// start on the final master).
	TimelineStartUS int64 `json:"timeline_start_us"`
	// AudioStartUS/AudioEndUS are the FINAL combined-timeline positions —
	// timeline_start + local span. These are the values the overlays use.
	AudioStartUS int64 `json:"audio_start_us"`
	AudioEndUS   int64 `json:"audio_end_us"`

	VoiceoverAssetID string  `json:"voiceover_asset_id,omitempty"`
	Confidence       float64 `json:"confidence"`
}

// Validate enforces the entity-occurrence projection invariants: non-empty
// identity, valid rune span, valid word span, non-negative monotonic
// microsecond ranges, and the canonical local→global mapping. A consumer can
// never trust a timestamp that drifted from that mapping.
func (o EntityOccurrence) Validate() error {
	if strings.TrimSpace(o.EntityID) == "" {
		return fmt.Errorf("%w: empty entity id", ErrInvalidEntityTimeline)
	}
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("%w: empty entity name", ErrInvalidEntityTimeline)
	}
	if strings.TrimSpace(o.Type) == "" {
		return fmt.Errorf("%w: empty entity type", ErrInvalidEntityTimeline)
	}
	if strings.TrimSpace(o.SceneID) == "" {
		return fmt.Errorf("%w: empty scene id", ErrInvalidEntityTimeline)
	}
	if o.SceneIndex < 0 {
		return fmt.Errorf("%w: negative scene index %d", ErrInvalidEntityTimeline, o.SceneIndex)
	}
	if o.TextStart < 0 || o.TextEnd <= o.TextStart {
		return fmt.Errorf("%w: entity %q invalid text span [%d,%d)", ErrInvalidEntityTimeline, o.Name, o.TextStart, o.TextEnd)
	}
	if o.WordStart < 0 || o.WordEnd < o.WordStart {
		return fmt.Errorf("%w: entity %q invalid word span [%d,%d]", ErrInvalidEntityTimeline, o.Name, o.WordStart, o.WordEnd)
	}
	if o.LocalStartUS < 0 || o.LocalEndUS < o.LocalStartUS {
		return fmt.Errorf("%w: entity %q inverted local span [%d,%d)", ErrInvalidEntityTimeline, o.Name, o.LocalStartUS, o.LocalEndUS)
	}
	if o.TimelineStartUS < 0 {
		return fmt.Errorf("%w: entity %q negative timeline start %d", ErrInvalidEntityTimeline, o.Name, o.TimelineStartUS)
	}
	if o.AudioStartUS != o.TimelineStartUS+o.LocalStartUS {
		return fmt.Errorf("%w: entity %q audio start %d != timeline %d + local %d", ErrInvalidEntityTimeline, o.Name, o.AudioStartUS, o.TimelineStartUS, o.LocalStartUS)
	}
	if o.AudioEndUS != o.TimelineStartUS+o.LocalEndUS {
		return fmt.Errorf("%w: entity %q audio end %d != timeline %d + local %d", ErrInvalidEntityTimeline, o.Name, o.AudioEndUS, o.TimelineStartUS, o.LocalEndUS)
	}
	if o.Confidence < 0 || o.Confidence > 1 {
		return fmt.Errorf("%w: entity %q confidence %f out of range", ErrInvalidEntityTimeline, o.Name, o.Confidence)
	}
	return nil
}

// SceneEntityTimeline bundles the entity occurrences of one scene together
// with the scene's canonical timeline offset, so a consumer can prove every
// occurrence in the scene shares the same offset.
type SceneEntityTimeline struct {
	SceneID          string             `json:"scene_id"`
	SceneIndex       int                `json:"scene_index"`
	TimelineStartUS  int64              `json:"timeline_start_us"`
	VoiceoverAssetID string             `json:"voiceover_asset_id,omitempty"`
	Entities         []EntityOccurrence `json:"entities"`
}

// EntityTimeline is the canonical scene-level projection of the run's
// entities onto the final combined timeline. It is the SSOT consumed by the
// overlay resolver: every occurrence carries real word-timing boundaries,
// never text-length estimates.
type EntityTimeline struct {
	Version    int                   `json:"version"`
	ProjectID  string                `json:"project_id,omitempty"`
	Language   string                `json:"language,omitempty"`
	DurationUS int64                 `json:"duration_us"`
	Scenes     []SceneEntityTimeline `json:"scenes"`
}

var (
	// ErrInvalidEntityTimeline is returned when an EntityTimeline (or an
	// occurrence inside it) violates its projection invariants.
	ErrInvalidEntityTimeline = errors.New("invalid entity timeline")
	// ErrEntityNotSpoken is returned when an extracted entity does not occur
	// verbatim in the canonical word timing. No timestamp is ever fabricated
	// for an entity the voiceover did not actually speak.
	ErrEntityNotSpoken = errors.New("entity not spoken in voiceover word timing")
	// ErrEntityNotInText is returned when an entity does not occur verbatim
	// in the scene text the voiceover was synthesized from.
	ErrEntityNotInText = errors.New("entity not found in scene text")
)

// Validate enforces the timeline projection invariants:
//
//   - schema version, non-negative duration;
//   - scenes strictly ordered by index with unique ids and non-negative
//     canonical offsets;
//   - every occurrence independently satisfies the local→global mapping and
//     stays within the certified timeline duration.
func (t EntityTimeline) Validate() error {
	if t.Version != EntityTimelineVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidEntityTimeline, t.Version)
	}
	if t.DurationUS <= 0 {
		return fmt.Errorf("%w: non-positive duration %d", ErrInvalidEntityTimeline, t.DurationUS)
	}
	seenIDs := make(map[string]struct{}, len(t.Scenes))
	previousIndex := -1
	for _, scene := range t.Scenes {
		if strings.TrimSpace(scene.SceneID) == "" {
			return fmt.Errorf("%w: scene with empty id", ErrInvalidEntityTimeline)
		}
		if _, dup := seenIDs[scene.SceneID]; dup {
			return fmt.Errorf("%w: duplicate scene %q", ErrInvalidEntityTimeline, scene.SceneID)
		}
		seenIDs[scene.SceneID] = struct{}{}
		if scene.SceneIndex <= previousIndex {
			return fmt.Errorf("%w: scenes not strictly ordered by index", ErrInvalidEntityTimeline)
		}
		previousIndex = scene.SceneIndex
		if scene.TimelineStartUS < 0 {
			return fmt.Errorf("%w: scene %q negative timeline start %d", ErrInvalidEntityTimeline, scene.SceneID, scene.TimelineStartUS)
		}
		for _, occurrence := range scene.Entities {
			if err := occurrence.Validate(); err != nil {
				return fmt.Errorf("%w: scene %q: %v", ErrInvalidEntityTimeline, scene.SceneID, err)
			}
			if occurrence.TimelineStartUS != scene.TimelineStartUS {
				return fmt.Errorf("%w: scene %q occurrence %q carries a different timeline offset", ErrInvalidEntityTimeline, scene.SceneID, occurrence.Name)
			}
			if occurrence.AudioEndUS > t.DurationUS {
				return fmt.Errorf("%w: entity %q audio end %d past timeline duration %d", ErrInvalidEntityTimeline, occurrence.Name, occurrence.AudioEndUS, t.DurationUS)
			}
		}
	}
	return nil
}
