package audio

import "fmt"

// Compile builds the complete audio plan from the already-resolved canonical
// video timeline. It never derives timing from assets or scene metadata.
func Compile(t CanonicalTimeline, profile CanonicalAudioProfile) (CompiledAudioPlan, error) {
	if err := t.Validate(); err != nil {
		return CompiledAudioPlan{}, err
	}
	p := CompiledAudioPlan{Version: AudioPlanVersion, TimelineVersion: t.Version, DurationMS: t.DurationMS, Output: profile.Output()}
	for _, s := range t.Segments {
		e := AudioEvent{TimelineStartMS: s.TimelineStartMS, DurationMS: s.DurationMS, SourceInMS: s.Audio.SourceInMS, SourceOutMS: s.Audio.SourceOutMS, GainDB: s.Audio.GainDB}
		switch s.Audio.Mode {
		case AudioVoiceover:
			e.Type, e.AssetID = EventVoiceover, s.Audio.VoiceoverAssetID
		case AudioClip:
			e.Type, e.AssetID = EventClip, s.Audio.ClipAssetID
		case AudioSilence:
			e.Type = EventSilence
		}
		// Voiceover assets are trimmed against the canonical segment window.
		// Materialising the range here keeps source timing out of the FFmpeg
		// filtergraph and makes every non-silence event self-contained.
		if e.Type == EventVoiceover && e.SourceOutMS == 0 {
			e.SourceOutMS = e.SourceInMS + e.DurationMS
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
