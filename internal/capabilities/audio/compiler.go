package audio

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
)

// Compile builds the complete primary-event audio plan from the
// already-resolved canonical video timeline. It never derives timing from
// assets or scene metadata.
func Compile(t CanonicalTimeline, profile CanonicalAudioProfile) (CompiledAudioPlan, error) {
	return CompileWithLayers(t, profile, nil, nil, nil)
}

// CompileWithMixPolicy builds the primary-event plan and applies the given
// mix policy. VOICEOVER_ONLY drops clip audio; VOICEOVER_DUCKED_CLIP keeps
// the voiceover at unity and ducks the original clip audio underneath it with
// a static gain plus dynamic ducking automation. The chosen policy is recorded
// on the plan so the mixer and renderer consume the same decision.
func CompileWithMixPolicy(t CanonicalTimeline, profile CanonicalAudioProfile, policy audio.AudioMixPolicy) (CompiledAudioPlan, error) {
	return compilePlan(t, profile, nil, nil, nil, policy)
}

// CompileWithLayers extends Compile with already-resolved BGM and SFX layers.
// Random selection must happen before this boundary; the compiled plan only
// contains concrete asset IDs and integer timeline ranges. The same canonical
// timeline still owns every primary event offset.
func CompileWithLayers(t CanonicalTimeline, profile CanonicalAudioProfile, bgm, sfx []AudioLayer, automation []AudioAutomation) (CompiledAudioPlan, error) {
	return compilePlan(t, profile, bgm, sfx, automation, "")
}

// CompileWithLayersAndPolicy extends CompileWithLayers with an explicit
// editorial mix policy (VOICEOVER_ONLY / VOICEOVER_DUCKED_CLIP), recorded
// on the plan so the mixer and renderer consume the same decision. An
// empty policy preserves the legacy full-volume overlap behaviour.
func CompileWithLayersAndPolicy(t CanonicalTimeline, profile CanonicalAudioProfile, bgm, sfx []AudioLayer, automation []AudioAutomation, policy audio.AudioMixPolicy) (CompiledAudioPlan, error) {
	return compilePlan(t, profile, bgm, sfx, automation, policy)
}

func compilePlan(t CanonicalTimeline, profile CanonicalAudioProfile, bgm, sfx []AudioLayer, automation []AudioAutomation, policy audio.AudioMixPolicy) (CompiledAudioPlan, error) {
	if err := t.Validate(); err != nil {
		return CompiledAudioPlan{}, err
	}
	p := CompiledAudioPlan{
		Version:         AudioPlanVersion,
		TimelineVersion: t.Version,
		DurationUS:      t.DurationUS,
		Output:          profile.Output(),
		Automation:      append([]AudioAutomation(nil), automation...),
		MixPolicy:       policy.Normalize(),
	}
	for _, s := range t.Segments {
		for i, intent := range s.EffectiveAudioIntents() {
			durationUS := s.DurationUS
			if intent.TimelineDurationUS > 0 {
				durationUS = intent.TimelineDurationUS
			}
			e := AudioEvent{EventID: fmt.Sprintf("%s-%s-%d", s.ID, strings.ToLower(string(intent.Mode)), i), TimelineStartUS: s.TimelineStartUS + intent.TimelineOffsetUS, DurationUS: durationUS, SourceInUS: intent.SourceInUS, SourceDurationUS: intent.SourceDurationUS, UseOriginalAudio: intent.UseOriginalAudio, GainDB: intent.GainDB}
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
	// BGM/SFX levels are policy, not user-controlled asset metadata. Keep the
	// payload gain fields for wire compatibility, but normalize them here so
	// every producer (including translated runs) reaches the same master mix.
	for i, layer := range bgm {
		track := findOrCreateLayerTrack(&p.Tracks, TrackBGM, "bgm")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("bgm-%d", i), Type: EventBGM, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceDurationUS: layer.DurationUS, GainDB: audio.BackgroundMusicGainDB})
	}
	for i, layer := range sfx {
		track := findOrCreateLayerTrack(&p.Tracks, TrackSFX, "sfx")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("sfx-%d", i), Type: EventSFX, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceInUS: layer.SourceInUS, SourceDurationUS: layer.DurationUS, GainDB: audio.SoundEffectGainDB})
	}
	applyMixPolicy(&p)
	if err := p.Seal(); err != nil {
		return CompiledAudioPlan{}, err
	}
	return p, nil
}

// applyMixPolicy mutates the compiled plan in place to honour its recorded
// mix policy. It runs after all primary and layer events are materialized so
// it can see whether a voiceover is actually present before ducking the clip.
func applyMixPolicy(p *CompiledAudioPlan) {
	switch p.MixPolicy {
	case audio.MixVoiceoverOnly:
		removeTrack(&p.Tracks, TrackClipAudio)
	case audio.MixVoiceoverWithDuckedClip:
		if findTrack(p.Tracks, TrackVoiceover) == nil {
			return // nothing to duck under
		}
		clip := findTrack(p.Tracks, TrackClipAudio)
		if clip == nil {
			return
		}
		for i := range clip.Events {
			if clip.Events[i].GainDB == 0 {
				clip.Events[i].GainDB = audio.DuckClipBaseGainDB
			}
		}
		p.Automation = append(p.Automation, clipDuckingAutomation(p.Tracks)...)
	}
}

// clipDuckingAutomation lowers the clip-audio track while the voiceover is
// speaking. Every overlapping clip/voiceover interval produces one automation
// entry so the clip returns to its base gain outside the narration.
func clipDuckingAutomation(tracks []AudioTrack) []AudioAutomation {
	vo := findTrack(tracks, TrackVoiceover)
	clip := findTrack(tracks, TrackClipAudio)
	if vo == nil || clip == nil {
		return nil
	}
	// The two track IDs are invariant across the entire duck zone. Hoist
	// the lowercase conversions out of the nested clip×voiceover loop so
	// the compiler does not re-allocate the same two strings for every
	// overlapping pair (GC pressure on long ducked mixes).
	clipTrackID := strings.ToLower(string(TrackClipAudio))
	voTrackID := strings.ToLower(string(TrackVoiceover))
	var out []AudioAutomation
	for _, ce := range clip.Events {
		clipEnd := ce.TimelineStartUS + ce.DurationUS
		for _, ve := range vo.Events {
			// Duck only while speech is actually present. DurationUS is the
			// scene window; SourceDurationUS is the certified speech length.
			// The duck zone ends at the shorter of the two so the clip returns
			// to its base gain as soon as the narration stops instead of
			// staying ducked for the whole scene window.
			voEnd := ve.TimelineStartUS + min(ve.DurationUS, ve.SourceDurationUS)
			start := ce.TimelineStartUS
			if ve.TimelineStartUS > start {
				start = ve.TimelineStartUS
			}
			end := clipEnd
			if voEnd < end {
				end = voEnd
			}
			if end <= start {
				continue
			}
			out = append(out, AudioAutomation{
				TargetTrackID:  clipTrackID,
				TriggerTrackID: voTrackID,
				StartUS:        start,
				EndUS:          end,
				GainDB:         audio.DuckClipActiveGainDB,
				AttackUS:       audio.DuckAttackUS,
				ReleaseUS:      audio.DuckReleaseUS,
			})
		}
	}
	return out
}

func findTrack(tracks []AudioTrack, role AudioTrackRole) *AudioTrack {
	for i := range tracks {
		if tracks[i].Role == role {
			return &tracks[i]
		}
	}
	return nil
}

func removeTrack(tracks *[]AudioTrack, role AudioTrackRole) {
	out := (*tracks)[:0]
	for _, tr := range *tracks {
		if tr.Role != role {
			out = append(out, tr)
		}
	}
	*tracks = out
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
