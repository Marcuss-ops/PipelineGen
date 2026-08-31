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
		intents := s.EffectiveAudioIntents()
		fixedMedia := hasProtectedOriginalAudio(intents)
		for i, intent := range intents {
			// A protected fixed-media scene is authoritative: an accidental
			// generated voiceover intent must not reach the plan.
			if fixedMedia && !intent.ProtectedOriginalAudio {
				continue
			}
			durationUS := s.DurationUS
			if intent.TimelineDurationUS > 0 {
				durationUS = intent.TimelineDurationUS
			}
			e := AudioEvent{
				EventID:                fmt.Sprintf("%s-%s-%d", s.ID, strings.ToLower(string(intent.Mode)), i),
				TimelineStartUS:        s.TimelineStartUS + intent.TimelineOffsetUS,
				DurationUS:             durationUS,
				SourceInUS:             intent.SourceInUS,
				SourceDurationUS:       intent.SourceDurationUS,
				UseOriginalAudio:       intent.UseOriginalAudio,
				ProtectedOriginalAudio: intent.ProtectedOriginalAudio,
				GainDB:                 intent.GainDB,
			}
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
	protected := protectedAudioWindows(t)
	filteredBGM := filterAudioLayersOutsideProtectedWindows(bgm, protected)
	filteredSFX := filterAudioLayersOutsideProtectedWindows(sfx, protected)
	for i, layer := range filteredBGM {
		track := findOrCreateLayerTrack(&p.Tracks, TrackBGM, "bgm")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("bgm-%d", i), Type: EventBGM, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceInUS: layer.SourceInUS, SourceDurationUS: layer.DurationUS, GainDB: audio.BackgroundMusicGainDB})
	}
	for i, layer := range filteredSFX {
		track := findOrCreateLayerTrack(&p.Tracks, TrackSFX, "sfx")
		track.Events = append(track.Events, AudioEvent{EventID: fmt.Sprintf("sfx-%d", i), Type: EventSFX, AssetID: layer.AssetID, TimelineStartUS: layer.TimelineStartUS, DurationUS: layer.DurationUS, SourceInUS: layer.SourceInUS, SourceDurationUS: layer.DurationUS, GainDB: audio.SoundEffectGainDB})
	}
	p.Automation = filterAutomationOutsideProtectedWindows(p.Automation, protected)
	applyMixPolicy(&p)
	if err := p.Seal(); err != nil {
		return CompiledAudioPlan{}, err
	}
	return p, nil
}

// applyMixPolicy mutates the compiled plan in place to honour its recorded
// mix policy. It runs after all primary and layer events are materialized so
// it can see whether a voiceover is actually present before ducking the clip.
type audioWindow struct {
	start int64
	end   int64
}

func hasProtectedOriginalAudio(intents []AudioIntent) bool {
	for _, intent := range intents {
		if intent.ProtectedOriginalAudio {
			return true
		}
	}
	return false
}

func protectedAudioWindows(t CanonicalTimeline) []audioWindow {
	var out []audioWindow
	for _, segment := range t.Segments {
		if !hasProtectedOriginalAudio(segment.EffectiveAudioIntents()) {
			continue
		}
		out = append(out, audioWindow{start: segment.TimelineStartUS, end: segment.TimelineStartUS + segment.DurationUS})
	}
	return out
}

func overlapsProtectedWindow(start, end int64, protected []audioWindow) bool {
	for _, window := range protected {
		if start < window.end && window.start < end {
			return true
		}
	}
	return false
}

// filterAudioLayersOutsideProtectedWindows cuts layers around protected
// fixed-media spans. The resulting events remain explicit and deterministic:
// body audio is preserved, while no BGM/SFX source range can enter a fixed
// section.
func filterAudioLayersOutsideProtectedWindows(layers []AudioLayer, protected []audioWindow) []AudioLayer {
	if len(protected) == 0 {
		return layers
	}
	out := make([]AudioLayer, 0, len(layers))
	for _, layer := range layers {
		segments := []audioWindow{{start: layer.TimelineStartUS, end: layer.TimelineStartUS + layer.DurationUS}}
		for _, blocked := range protected {
			segments = subtractAudioWindow(segments, blocked)
		}
		for _, segment := range segments {
			if segment.end <= segment.start {
				continue
			}
			offset := segment.start - layer.TimelineStartUS
			copy := layer
			copy.TimelineStartUS = segment.start
			copy.DurationUS = segment.end - segment.start
			copy.SourceInUS += offset
			out = append(out, copy)
		}
	}
	return out
}

func subtractAudioWindow(source []audioWindow, blocked audioWindow) []audioWindow {
	out := make([]audioWindow, 0, len(source)+1)
	for _, segment := range source {
		if blocked.end <= segment.start || blocked.start >= segment.end {
			out = append(out, segment)
			continue
		}
		if segment.start < blocked.start {
			out = append(out, audioWindow{start: segment.start, end: blocked.start})
		}
		if blocked.end < segment.end {
			out = append(out, audioWindow{start: blocked.end, end: segment.end})
		}
	}
	return out
}

func filterAutomationOutsideProtectedWindows(automation []AudioAutomation, protected []audioWindow) []AudioAutomation {
	if len(protected) == 0 {
		return automation
	}
	out := make([]AudioAutomation, 0, len(automation))
	for _, item := range automation {
		segments := []audioWindow{{start: item.StartUS, end: item.EndUS}}
		for _, blocked := range protected {
			segments = subtractAudioWindow(segments, blocked)
		}
		for _, segment := range segments {
			if segment.end <= segment.start {
				continue
			}
			copy := item
			copy.StartUS = segment.start
			copy.EndUS = segment.end
			out = append(out, copy)
		}
	}
	return out
}

func applyMixPolicy(p *CompiledAudioPlan) {
	switch p.MixPolicy {
	case audio.MixVoiceoverOnly:
		removeUnprotectedClipEvents(&p.Tracks)
		removeEmptyTracks(&p.Tracks, TrackClipAudio)
	case audio.MixVoiceoverWithDuckedClip:
		if findTrack(p.Tracks, TrackVoiceover) == nil {
			return // nothing to duck under
		}
		clip := findTrack(p.Tracks, TrackClipAudio)
		if clip == nil {
			return
		}
		for i := range clip.Events {
			if clip.Events[i].ProtectedOriginalAudio {
				continue
			}
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
		if ce.ProtectedOriginalAudio {
			continue
		}
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

func removeUnprotectedClipEvents(tracks *[]AudioTrack) {
	for i := range *tracks {
		if (*tracks)[i].Role != TrackClipAudio {
			continue
		}
		out := (*tracks)[i].Events[:0]
		for _, event := range (*tracks)[i].Events {
			if event.ProtectedOriginalAudio {
				out = append(out, event)
			}
		}
		(*tracks)[i].Events = out
	}
}

func removeEmptyTracks(tracks *[]AudioTrack, role AudioTrackRole) {
	out := (*tracks)[:0]
	for _, track := range *tracks {
		if track.Role == role && len(track.Events) == 0 {
			continue
		}
		out = append(out, track)
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
