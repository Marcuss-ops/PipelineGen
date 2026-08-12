package audio

import "fmt"

// Compile builds the complete audio plan from the already-resolved canonical
// video timeline. It never derives timing from assets or scene metadata.
func Compile(t CanonicalTimeline, profile CanonicalAudioProfile) (CompiledAudioPlan, error) {
	if err := t.Validate(); err != nil {
		return CompiledAudioPlan{}, err
	}
	p := CompiledAudioPlan{Version: AudioPlanVersion, TimelineVersion: t.Version, DurationUS: t.DurationUS, Output: profile.Output()}
	for _, s := range t.Segments {
		e := AudioEvent{
			TimelineStartUS:  s.TimelineStartUS,
			DurationUS:       s.DurationUS,
			SourceInUS:       s.Audio.SourceInUS,
			SourceDurationUS: s.Audio.SourceDurationUS,
			GainDB:           s.Audio.GainDB,
		}
		switch s.Audio.Mode {
		case AudioVoiceover:
			e.Type, e.AssetID = EventVoiceover, s.Audio.VoiceoverAssetID
		case AudioClip:
			e.Type, e.AssetID = EventClip, s.Audio.ClipAssetID
		case AudioSilence:
			e.Type = EventSilence
		}
		// Voiceover assets occupy the canonical segment window. The source
		// range is materialized once here; downstream mixers do not infer it.
		if e.Type == EventVoiceover && e.SourceDurationUS == 0 {
			e.SourceDurationUS = e.DurationUS
		}
		if e.Type != EventSilence && e.AssetID == "" {
			return CompiledAudioPlan{}, fmt.Errorf("segment %s: audio asset is required for %s", s.ID, e.Type)
		}
		p.Events = append(p.Events, e)
	}
	if err := p.Seal(); err != nil {
		return CompiledAudioPlan{}, err
	}
	return p, nil
}
