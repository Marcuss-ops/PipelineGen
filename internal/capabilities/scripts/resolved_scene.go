package scriptgeneration

import (
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ResolvedScene is the sealed technical projection of an editorial Scene.
// Once created, downstream timeline/render/audio code must not read legacy
// millisecond fields or re-derive duration from a clip.
type ResolvedScene struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	// TimelineStartUS is the absolute timeline placement of this scene: the
	// cumulative sum of all preceding scene durations. The scene end is always
	// derived as TimelineStartUS + DurationUS and is never stored.
	TimelineStartUS int64                `json:"timeline_start_us"`
	Text            map[Language]string  `json:"text"`
	DurationUS      int64                `json:"duration_us"`
	Video           audio.VideoSegment   `json:"video"`
	VideoSegments   []audio.VideoSegment `json:"video_segments,omitempty"`
	ClipAudioPaths  map[string]string    `json:"clip_audio_paths,omitempty"`
	Voiceover       *ResolvedVoiceover   `json:"voiceover,omitempty"`
	AudioIntents    []audio.AudioIntent  `json:"audio_intents"`
}

type ResolvedVoiceover struct {
	AssetID    string `json:"asset_id"`
	Path       string `json:"path"`
	DurationUS int64  `json:"duration_us"`
}

// SceneDurationResolver is the single policy point for scene timeline
// duration. The explicit editorial duration wins; legacy milliseconds and
// clip duration are accepted only while resolving the boundary input.
type SceneDurationResolver struct{}

func (SceneDurationResolver) Resolve(scene Scene, voiceoverDurationUS int64) (int64, error) {
	if scene.DurationUS > 0 {
		return scene.DurationUS, nil
	}
	if scene.DurationMS > 0 {
		return checkedUS(scene.DurationMS, 1000, "duration_ms")
	}
	if len(scene.Clips) > 0 {
		var total int64
		for _, clip := range scene.Clips {
			if clip != nil && clip.Duration > 0 {
				total += checkedFloatSeconds(clip.Duration, "clip duration")
			}
		}
		if total > 0 {
			return total, nil
		}
	}
	if scene.Clip != nil && scene.Clip.Duration > 0 {
		return checkedFloatSeconds(scene.Clip.Duration, "clip duration"), nil
	}
	if voiceoverDurationUS > 0 {
		return voiceoverDurationUS, nil
	}
	return 0, fmt.Errorf("scene %s has no resolved editorial duration", scene.ID)
}

// ResolveScenes seals the asset/timing projection. It is deliberately pure:
// asset acquisition belongs to the adapter/Media Registry boundary, while
// this function only accepts already-resolved references.
func ResolveScenes(scenes []Scene, language Language) ([]ResolvedScene, error) {
	resolved := make([]ResolvedScene, 0, len(scenes))
	durationResolver := SceneDurationResolver{}
	var startUS int64
	for i, scene := range scenes {
		if scene.Index != i || strings.TrimSpace(scene.ID) == "" {
			return nil, fmt.Errorf("scene %d has invalid id or index", i)
		}
		var vo *ResolvedVoiceover
		if ref, ok := scene.Voiceover[language]; ok && ref.ID != "" {
			durationUS := checkedFloatSeconds(ref.Duration, "voiceover duration")
			if durationUS > 0 {
				vo = &ResolvedVoiceover{AssetID: ref.ID, Path: ref.FilePath, DurationUS: durationUS}
			}
		}
		durationUS, err := durationResolver.Resolve(scene, func() int64 {
			if vo != nil {
				return vo.DurationUS
			}
			return 0
		}())
		if err != nil {
			return nil, err
		}
		intents := append([]audio.AudioIntent(nil), scene.AudioIntents...)
		if len(intents) == 0 {
			intent := scene.Audio
			if intent.Mode == "" {
				intent.Mode = audio.AudioSilence
			}
			intents = []audio.AudioIntent{intent}
		}
		videos, err := resolvedVideos(scene, intents)
		if err != nil {
			return nil, err
		}
		var video audio.VideoSegment
		if len(videos) > 0 {
			video = videos[0]
		}
		clipAudioPaths := make(map[string]string)
		clips := scene.Clips
		if len(clips) == 0 && scene.Clip != nil {
			clips = []*ClipReference{scene.Clip}
		}
		for _, clip := range clips {
			if clip != nil && clip.ID != "" {
				clipAudioPaths[clip.ID] = clip.AudioPath
			}
		}
		resolved = append(resolved, ResolvedScene{ID: scene.ID, Index: i, TimelineStartUS: startUS, Text: scene.Text, DurationUS: durationUS, Video: video, VideoSegments: videos, ClipAudioPaths: clipAudioPaths, Voiceover: vo, AudioIntents: intents})
		if startUS > math.MaxInt64-durationUS {
			return nil, fmt.Errorf("scene %s timeline start overflows", scene.ID)
		}
		startUS += durationUS
	}
	return resolved, nil
}

func checkedUS(value, multiplier int64, field string) (int64, error) {
	if value <= 0 || value > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("%s is out of range", field)
	}
	return value * multiplier, nil
}

func checkedFloatSeconds(value float64, field string) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > float64(math.MaxInt64)/1_000_000 {
		return 0
	}
	return int64(math.Round(value * 1_000_000))
}

func resolvedVideos(scene Scene, intents []audio.AudioIntent) ([]audio.VideoSegment, error) {
	clips := scene.Clips
	if len(clips) == 0 && scene.Clip != nil {
		clips = []*ClipReference{scene.Clip}
	}
	videos := make([]audio.VideoSegment, 0, len(clips))
	var offset int64
	for _, clip := range clips {
		if clip == nil {
			continue
		}
		if clip.SourceInMS < 0 || clip.SourceOutMS < 0 || clip.SourceOutMS < clip.SourceInMS {
			return nil, fmt.Errorf("scene %s has invalid clip source range", scene.ID)
		}
		inUS := clip.SourceInMS * 1000
		durationUS := (clip.SourceOutMS - clip.SourceInMS) * 1000
		if durationUS <= 0 {
			for _, intent := range intents {
				if intent.Mode == audio.AudioClip && intent.ClipAssetID == clip.ID {
					inUS, durationUS = intent.SourceInUS, intent.SourceDurationUS
					break
				}
			}
		}
		if durationUS <= 0 {
			return nil, fmt.Errorf("scene %s clip %s has no usable duration", scene.ID, clip.ID)
		}
		videos = append(videos, audio.VideoSegment{AssetID: clip.ID, SourceInUS: inUS, SourceDurationUS: durationUS, TimelineOffsetUS: offset, TimelineDurationUS: durationUS})
		offset += durationUS
	}
	return videos, nil
}
