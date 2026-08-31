package audio

import (
	"encoding/json"
	"fmt"
	"math"
)

// LegacyTimelineDTO is the migration-window wire shape. It is deliberately
// not used by internal code; callers must normalize it into CanonicalTimeline.
type LegacyTimelineDTO struct {
	Version    string                  `json:"version"`
	DurationMS *int64                  `json:"duration_ms,omitempty"`
	DurationUS *int64                  `json:"duration_us,omitempty"`
	Segments   []LegacyTimelineSegment `json:"segments"`
}

type LegacyTimelineSegment struct {
	ID              string             `json:"id"`
	Index           int                `json:"index"`
	TimelineStartMS *int64             `json:"timeline_start_ms,omitempty"`
	TimelineStartUS *int64             `json:"timeline_start_us,omitempty"`
	DurationMS      *int64             `json:"duration_ms,omitempty"`
	DurationUS      *int64             `json:"duration_us,omitempty"`
	Video           LegacyVideoSegment `json:"video"`
	Audio           LegacyAudioIntent  `json:"audio"`
}

type LegacyVideoSegment struct {
	AssetID          string `json:"asset_id,omitempty"`
	SourceInMS       *int64 `json:"source_in_ms,omitempty"`
	SourceInUS       *int64 `json:"source_in_us,omitempty"`
	SourceOutMS      *int64 `json:"source_out_ms,omitempty"`
	SourceDurationUS *int64 `json:"source_duration_us,omitempty"`
}

type LegacyAudioIntent struct {
	Mode                   AudioSegmentMode `json:"mode"`
	VoiceoverAssetID       string           `json:"voiceover_asset_id,omitempty"`
	ClipAssetID            string           `json:"clip_asset_id,omitempty"`
	SourceInMS             *int64           `json:"source_in_ms,omitempty"`
	SourceInUS             *int64           `json:"source_in_us,omitempty"`
	SourceOutMS            *int64           `json:"source_out_ms,omitempty"`
	SourceDurationUS       *int64           `json:"source_duration_us,omitempty"`
	UseOriginalAudio       bool             `json:"use_original_audio,omitempty"`
	ProtectedOriginalAudio bool             `json:"protected_original_audio,omitempty"`
	GainDB                 float64          `json:"gain_db,omitempty"`
}

// NormalizationReport makes legacy field usage observable during the
// migration window. DeprecatedFields contains JSON paths that arrived as MS
// fields; it is intentionally not part of the canonical timeline hash.
type NormalizationReport struct {
	DeprecatedFields []string
}

// NormalizeTimelineJSON accepts canonical v2 or legacy v1 JSON. It rejects
// contradictory MS/US pairs instead of silently choosing one representation.
func NormalizeTimelineJSON(data []byte) (CanonicalTimeline, NormalizationReport, error) {
	var report NormalizationReport
	var raw struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CanonicalTimeline{}, report, fmt.Errorf("decode timeline envelope: %w", err)
	}
	if raw.Version == TimelineVersion {
		if hasLegacyTimelineFields(data) {
			return CanonicalTimeline{}, report, fmt.Errorf("unsupported legacy timeline fields in schema v2")
		}
		var timeline CanonicalTimeline
		if err := json.Unmarshal(data, &timeline); err != nil {
			return CanonicalTimeline{}, report, fmt.Errorf("decode canonical timeline v2: %w", err)
		}
		if err := timeline.Validate(); err != nil {
			return CanonicalTimeline{}, report, err
		}
		return timeline, report, nil
	}
	if raw.Version != "" && raw.Version != "canonical-timeline.v1" && raw.Version != "1" {
		return CanonicalTimeline{}, report, fmt.Errorf("unsupported timeline schema version %q", raw.Version)
	}
	var legacy LegacyTimelineDTO
	if err := json.Unmarshal(data, &legacy); err != nil {
		return CanonicalTimeline{}, report, fmt.Errorf("decode legacy timeline: %w", err)
	}
	timeline, report, err := NormalizeLegacyTimeline(legacy)
	if err != nil {
		return CanonicalTimeline{}, report, err
	}
	return timeline, report, nil
}

// NormalizeLegacyTimeline converts v1 milliseconds to canonical v2
// microseconds. It accepts coherent duplicate MS/US fields while recording
// their deprecated use, and rejects ambiguity.
func NormalizeLegacyTimeline(legacy LegacyTimelineDTO) (CanonicalTimeline, NormalizationReport, error) {
	var report NormalizationReport
	if legacy.Version != "canonical-timeline.v1" && legacy.Version != "1" && legacy.Version != "" {
		return CanonicalTimeline{}, report, fmt.Errorf("unsupported timeline schema version %q", legacy.Version)
	}
	duration, usedMS, err := normalizeValue(legacy.DurationMS, legacy.DurationUS, "duration")
	if err != nil {
		return CanonicalTimeline{}, report, err
	}
	if usedMS {
		report.DeprecatedFields = append(report.DeprecatedFields, "duration_ms")
	}
	if len(legacy.Segments) == 0 {
		return CanonicalTimeline{}, report, fmt.Errorf("%w: no segments", ErrInvalidTimeline)
	}
	timeline := CanonicalTimeline{Version: TimelineVersion, DurationUS: duration, Segments: make([]TimelineSegment, 0, len(legacy.Segments))}
	for i, segment := range legacy.Segments {
		start, startMS, err := normalizeValue(segment.TimelineStartMS, segment.TimelineStartUS, fmt.Sprintf("segments[%d].timeline_start", i))
		if err != nil {
			return CanonicalTimeline{}, report, err
		}
		if startMS {
			report.DeprecatedFields = append(report.DeprecatedFields, fmt.Sprintf("segments[%d].timeline_start_ms", i))
		}
		dur, durMS, err := normalizeValue(segment.DurationMS, segment.DurationUS, fmt.Sprintf("segments[%d].duration", i))
		if err != nil {
			return CanonicalTimeline{}, report, err
		}
		if durMS {
			report.DeprecatedFields = append(report.DeprecatedFields, fmt.Sprintf("segments[%d].duration_ms", i))
		}
		video, videoMS, err := normalizeVideo(segment.Video, dur)
		if err != nil {
			return CanonicalTimeline{}, report, err
		}
		if videoMS {
			report.DeprecatedFields = append(report.DeprecatedFields, fmt.Sprintf("segments[%d].video", i))
		}
		audio, audioMS, err := normalizeAudio(segment.Audio, dur)
		if err != nil {
			return CanonicalTimeline{}, report, err
		}
		if audioMS {
			report.DeprecatedFields = append(report.DeprecatedFields, fmt.Sprintf("segments[%d].audio", i))
		}
		timeline.Segments = append(timeline.Segments, TimelineSegment{ID: segment.ID, Index: segment.Index, TimelineStartUS: start, DurationUS: dur, Video: video, Audio: audio})
	}
	if err := timeline.Validate(); err != nil {
		return CanonicalTimeline{}, report, err
	}
	return timeline, report, nil
}

func normalizeVideo(video LegacyVideoSegment, fallbackDuration int64) (VideoSegment, bool, error) {
	in, inMS, err := normalizeValue(video.SourceInMS, video.SourceInUS, "video.source_in")
	if err != nil {
		return VideoSegment{}, false, err
	}
	usedMS := inMS
	var duration int64
	if video.SourceDurationUS != nil {
		duration = *video.SourceDurationUS
		if duration < 0 {
			return VideoSegment{}, false, fmt.Errorf("video.source_duration_us must be non-negative")
		}
	}
	if video.SourceOutMS != nil {
		outUS, err := microsFromMillis(*video.SourceOutMS)
		if err != nil {
			return VideoSegment{}, false, err
		}
		if outUS <= in {
			return VideoSegment{}, false, fmt.Errorf("video source_out_ms must be greater than source_in")
		}
		if video.SourceDurationUS != nil && outUS-in != duration {
			return VideoSegment{}, false, fmt.Errorf("ambiguous video source range: source_out_ms disagrees with source_duration_us")
		}
		duration = outUS - in
		usedMS = true
	}
	if video.AssetID != "" && duration == 0 {
		duration = fallbackDuration
	}
	return VideoSegment{AssetID: video.AssetID, SourceInUS: in, SourceDurationUS: duration}, usedMS, nil
}

func normalizeAudio(audio LegacyAudioIntent, fallbackDuration int64) (AudioIntent, bool, error) {
	in, inMS, err := normalizeValue(audio.SourceInMS, audio.SourceInUS, "audio.source_in")
	if err != nil {
		return AudioIntent{}, false, err
	}
	usedMS := inMS
	duration := int64(0)
	if audio.SourceDurationUS != nil {
		duration = *audio.SourceDurationUS
		if duration < 0 {
			return AudioIntent{}, false, fmt.Errorf("audio.source_duration_us must be non-negative")
		}
	}
	if audio.SourceOutMS != nil {
		outUS, err := microsFromMillis(*audio.SourceOutMS)
		if err != nil {
			return AudioIntent{}, false, err
		}
		if outUS <= in {
			return AudioIntent{}, false, fmt.Errorf("audio source_out_ms must be greater than source_in")
		}
		if audio.SourceDurationUS != nil && outUS-in != duration {
			return AudioIntent{}, false, fmt.Errorf("ambiguous audio source range: source_out_ms disagrees with source_duration_us")
		}
		duration = outUS - in
		usedMS = true
	}
	if audio.Mode == AudioVoiceover && duration == 0 {
		duration = fallbackDuration
	}
	return AudioIntent{Mode: audio.Mode, VoiceoverAssetID: audio.VoiceoverAssetID, ClipAssetID: audio.ClipAssetID, SourceInUS: in, SourceDurationUS: duration, UseOriginalAudio: audio.UseOriginalAudio, ProtectedOriginalAudio: audio.ProtectedOriginalAudio, GainDB: audio.GainDB}, usedMS, nil
}

func normalizeValue(ms, us *int64, path string) (int64, bool, error) {
	if ms == nil && us == nil {
		return 0, false, nil
	}
	if us != nil {
		if *us < 0 {
			return 0, false, fmt.Errorf("%s_us must be non-negative", path)
		}
	}
	if ms != nil {
		converted, err := microsFromMillis(*ms)
		if err != nil {
			return 0, false, fmt.Errorf("%s_ms: %w", path, err)
		}
		if us != nil && converted != *us {
			return 0, false, fmt.Errorf("ambiguous timeline field %s: ms=%d us=%d", path, *ms, *us)
		}
		return converted, true, nil
	}
	return *us, false, nil
}

// MicrosecondsFromMilliseconds is the explicit legacy-boundary conversion
// helper for producers whose input DTOs still expose milliseconds.
func MicrosecondsFromMilliseconds(milliseconds int64) (int64, error) {
	return microsFromMillis(milliseconds)
}

func microsFromMillis(milliseconds int64) (int64, error) {
	if milliseconds < 0 || milliseconds > math.MaxInt64/1000 {
		return 0, fmt.Errorf("milliseconds value %d cannot be represented as microseconds", milliseconds)
	}
	return milliseconds * 1000, nil
}

// MarshalTimelineV2 makes the schema transition explicit at boundary code;
// CanonicalTimeline's default JSON encoding is already the same v2 shape.
func MarshalTimelineV2(timeline CanonicalTimeline) ([]byte, error) {
	if err := timeline.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(timeline)
}

// IsCanonicalTimelineV2 reports whether data carries only the v2 marker. It
// does not validate the complete payload; NormalizeTimelineJSON does that.
func hasLegacyTimelineFields(data []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	legacyKeys := []string{"duration_ms", "timeline_start_ms", "source_in_ms", "source_out_ms"}
	for _, key := range legacyKeys {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	var segments []map[string]json.RawMessage
	if value, ok := raw["segments"]; ok && json.Unmarshal(value, &segments) == nil {
		for _, segment := range segments {
			for _, key := range legacyKeys {
				if _, ok := segment[key]; ok {
					return true
				}
			}
			for _, nestedKey := range []string{"video", "audio"} {
				var nested map[string]json.RawMessage
				if value, ok := segment[nestedKey]; ok && json.Unmarshal(value, &nested) == nil {
					for _, key := range legacyKeys[1:] {
						if _, ok := nested[key]; ok {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func IsCanonicalTimelineV2(data []byte) bool {
	var raw struct {
		Version string `json:"version"`
	}
	return json.Unmarshal(data, &raw) == nil && raw.Version == TimelineVersion
}
