package audio

import (
	"fmt"
	"strings"
)

// Compile builds the complete primary-event audio plan from the
// already-resolved canonical video timeline. It never derives timing from
// assets or scene metadata.
func Compile(t CanonicalTimeline, profile CanonicalAudioProfile) (CompiledAudioPlan, error) {
	return CompileWithLayers(t, profile, nil, nil, nil)
}

// CompileWithLayers extends Compile with already-resolved BGM and SFX layers.
// Random selection must happen before this boundary; the compiled plan only
// contains concrete asset IDs and integer timeline ranges. The same canonical
// timeline still owns every primary event offset.
func CompileWithLayers(t CanonicalTimeline, profile CanonicalAudioProfile, bgm, sfx []AudioLayer, automation []AudioAutomation) (CompiledAudioPlan, error) {
	if err := t.Validate(); err != nil {
		return CompiledAudioPlan{}, err
	}
	p := CompiledAudioPlan{
		Version:         AudioPlanVersion,
		TimelineVersion: t.Version,
		DurationUS:      t.DurationUS,
		Output:          profile.Output(),
		Automation:      append([]AudioAutomation(nil), automation...),
	}
	for _, s := range t.Segments {
		for _, intent := range s.EffectiveAudioIntents() {
			e := AudioEvent{EventID: fmt.Sprintf("%s-%s", s.ID, strings.ToLower(string(intent.Mode))), TimelineStartUS: s.TimelineStartUS, DurationUS: s.DurationUS, SourceInUS: intent.SourceInUS, SourceDurationUS: intent.SourceDurationUS, UseOriginalAudio: intent.UseOriginalAudio, GainDB: intent.GainDB}
			var role AudioTrackRole
			switch intent.Mode {
			case AudioVoiceover:
				e.Type, e.AssetID, role = EventVoiceover, intent.VoiceoverAssetID, TrackVoiceover
			case AudioClip:
				e.Type, e.AssetID, role = EventClip, intent.ClipAssetID, TrackClipAudio
				// A CLIP_AUDIO intent is explicitly the original source stream;
				// legacy callers omitted this redundant flag.
				e.UseOriginalAudio = true
			case AudioSilence:
				e.Type, role = EventSilence, TrackVoiceover
			default:
				return CompiledAudioPlan{}, fmt.Errorf("segment %s: unsupported audio mode %q", s.ID, intent.Mode)
			}
			if e.Type == EventVoiceover && e.SourceDurationUS == 0 {
				e.SourceDurationUS = e.DurationUS
			}
			if e.Type != EventSilence && e.AssetID == "" {
				return CompiledAudioPlan{}, fmt.Errorf("segment %s: audio asset is required for %s", s.ID, e.Type)
			}
			track := findOrCreateTrack(&p.Tracks, role)
			track.Events = append(track.Events, e)
		}
	}
	for i, layer := range bgm {
		track := findOrCreateLayerTrack(&p.Tracks, TrackBGM, "bgm")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("bgm-%d", i), Type: EventBGM, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceDurationUS: layer.DurationUS, GainDB: layer.GainDB})
	}
	for i, layer := range sfx {
		track := findOrCreateLayerTrack(&p.Tracks, TrackSFX, "sfx")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("sfx-%d", i), Type: EventSFX, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceDurationUS: layer.DurationUS, GainDB: layer.GainDB})
	}
	if err := p.Seal(); err != nil {
		return CompiledAudioPlan{}, err
	}
	return p, nil
}

func findOrCreateLayerTrack(tracks *[]AudioTrack, role AudioTrackRole, id string) *AudioTrack {
	for i := range *tracks {
		if (*tracks)[i].Role == role {
			return &(*tracks)[i]
		}
	}
	*tracks = append(*tracks, AudioTrack{TrackID: id, Role: role, OverlapPolicy: "ALLOW"})
	return &(*tracks)[len(*tracks)-1]
}

func findOrCreateTrack(tracks *[]AudioTrack, role AudioTrackRole) *AudioTrack {
	for i := range *tracks {
		if (*tracks)[i].Role == role {
			return &(*tracks)[i]
		}
	}
	*tracks = append(*tracks, AudioTrack{TrackID: strings.ToLower(string(role)), Role: role, OverlapPolicy: "EXCLUSIVE"})
	return &(*tracks)[len(*tracks)-1]
}
