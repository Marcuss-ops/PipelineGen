package scriptgeneration

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func eventsForRole(plan audio.CompiledAudioPlan, role audio.AudioTrackRole) []audio.AudioEvent {
	var events []audio.AudioEvent
	for _, track := range plan.Tracks {
		if track.Role == role {
			events = append(events, track.Events...)
		}
	}
	return events
}

func TestCompileCanonicalAudioPlanUsesOneTimelineForPrimaryEvents(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{
		{ID: "scene-001", Index: 0, DurationMS: 14000, Audio: audio.AudioIntent{Mode: audio.AudioVoiceover}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1", FilePath: "/audio/vo-1.mp3"}}},
		{ID: "scene-002", Index: 1, DurationMS: 12000, Clip: &ClipReference{ID: "clip-1", AudioPath: "/video/clip-1.mp4"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-1", SourceInUS: 34000000, SourceDurationUS: 12000000}},
		{ID: "scene-003", Index: 2, DurationMS: 2000, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
	}}
	timeline, plan, assets, err := CompileCanonicalAudioPlan(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if timeline.DurationUS != 28000000 || plan.DurationUS != timeline.DurationUS || len(eventsForRole(plan, audio.TrackVoiceover))+len(eventsForRole(plan, audio.TrackClipAudio)) != 3 {
		t.Fatalf("timeline=%+v plan=%+v", timeline, plan)
	}
	clipEvents := eventsForRole(plan, audio.TrackClipAudio)
	if clipEvents[0].TimelineStartUS != 14000000 || clipEvents[0].SourceInUS != 34000000 || clipEvents[0].SourceDurationUS != 12000000 {
		t.Fatalf("clip event=%+v", clipEvents[0])
	}
	if len(assets) != 2 || plan.PlanSHA256 == "" {
		t.Fatalf("assets=%+v hash=%q", assets, plan.PlanSHA256)
	}
}

// TestVoiceoverSourceDurationEqualsCleanedProbeNotClipDuration certifies the
// invariant from the audio/document verdetto: the voiceover source duration
// recorded on the canonical timeline must be the probed duration of the
// CLEANED voiceover file, never the clip duration. A 45s clip whose narration
// was cleaned to 30s must surface source_duration_us=30s, not 45s.
func TestVoiceoverSourceDurationEqualsCleanedProbeNotClipDuration(t *testing.T) {
	const clipDurationUS = 45_000_000 // the clip is 45s
	const cleanedVOUS = 30_000_000    // the CLEANED voiceover is only 30s

	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-combined", Index: 0, DurationUS: clipDurationUS,
		Clip:  &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4", SourceInMS: 0, SourceOutMS: 45000, Duration: 45},
		Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 0, SourceDurationUS: clipDurationUS, UseOriginalAudio: true},
		// Duration is the certified probe of the CLEANED file, not the clip.
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/a.m4a", Duration: 30.0}},
	}}}

	// The resolved probe of the cleaned voiceover.
	resolved, err := ResolveScenes(result.Scenes, "it", audio.AudioModeNone, false)
	if err != nil {
		t.Fatal(err)
	}
	require.NotNil(t, resolved[0].Voiceover)
	cleanedVoiceoverProbe := *resolved[0].Voiceover

	// The canonical timeline's voiceover intent.
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	var timelineVoiceover *audio.AudioIntent
	for i := range timeline.Segments[0].AudioIntents {
		if timeline.Segments[0].AudioIntents[i].Mode == audio.AudioVoiceover {
			timelineVoiceover = &timeline.Segments[0].AudioIntents[i]
			break
		}
	}
	require.NotNil(t, timelineVoiceover)

	// The VO source duration must be the measured cleaned-file duration,
	// never the clip duration.
	require.Equal(t, cleanedVoiceoverProbe.DurationUS, timelineVoiceover.SourceDurationUS)
	require.Equal(t, int64(cleanedVOUS), timelineVoiceover.SourceDurationUS)
	require.NotEqual(t, int64(clipDurationUS), timelineVoiceover.SourceDurationUS)
}

func TestCompileCanonicalAudioPlanMakesVoiceoverTimelinePlacementExplicit(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-vo", Index: 0, DurationUS: 4_500_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}},
	}}}
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	intents := timeline.Segments[0].EffectiveAudioIntents()
	if len(intents) != 1 {
		t.Fatalf("intents=%+v", intents)
	}
	vo := intents[0]
	if vo.Mode != audio.AudioVoiceover || vo.TimelineOffsetUS != 0 || vo.TimelineDurationUS != 4_500_000 || vo.SourceInUS != 0 || vo.SourceDurationUS != 3_200_000 {
		t.Fatalf("voiceover intent must carry explicit timeline placement: %+v", vo)
	}
}

func TestCanonicalAudioPlanVoiceoverIntentSerializesExplicitTimelinePlacement(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-vo", Index: 0, DurationUS: 4_500_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}},
	}}}
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Segments []struct {
			AudioIntents []struct {
				Mode               string `json:"mode"`
				TimelineOffsetUS   *int64 `json:"timeline_offset_us"`
				TimelineDurationUS *int64 `json:"timeline_duration_us"`
				SourceDurationUS   *int64 `json:"source_duration_us"`
			} `json:"audio_intents"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Segments) != 1 || len(wire.Segments[0].AudioIntents) != 1 {
		t.Fatalf("unexpected wire shape: %s", encoded)
	}
	vo := wire.Segments[0].AudioIntents[0]
	if vo.Mode != "VOICEOVER" {
		t.Fatalf("mode=%q", vo.Mode)
	}
	// timeline_offset_us must be explicit even when it is the scene origin (0).
	if vo.TimelineOffsetUS == nil || *vo.TimelineOffsetUS != 0 {
		t.Fatalf("timeline_offset_us must be explicit (got %v): %s", vo.TimelineOffsetUS, encoded)
	}
	if vo.TimelineDurationUS == nil || *vo.TimelineDurationUS != 4_500_000 {
		t.Fatalf("timeline_duration_us must be explicit (got %v): %s", vo.TimelineDurationUS, encoded)
	}
	if vo.SourceDurationUS == nil || *vo.SourceDurationUS != 3_200_000 {
		t.Fatalf("source_duration_us must be explicit (got %v): %s", vo.SourceDurationUS, encoded)
	}
}

func TestCompileCanonicalAudioPlanAppliesDuckedMixPolicyWithExplicitVOTiming(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-combined", Index: 0, DurationUS: 5_000_000,
		Clip:      &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4", SourceInMS: 1000, SourceOutMS: 6000},
		Audio:     audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 1_000_000, SourceDurationUS: 5_000_000, UseOriginalAudio: true},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/a.m4a", Duration: 4.0}},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}

	// The mix decision is recorded on the compiled plan.
	if plan.MixPolicy != audio.MixVoiceoverWithDuckedClip {
		t.Fatalf("mix policy = %q, want %q", plan.MixPolicy, audio.MixVoiceoverWithDuckedClip)
	}

	// The voiceover carries explicit timeline placement.
	intents := timeline.Segments[0].EffectiveAudioIntents()
	var vo, clip *audio.AudioIntent
	for i := range intents {
		switch intents[i].Mode {
		case audio.AudioVoiceover:
			vo = &intents[i]
		case audio.AudioClip:
			clip = &intents[i]
		}
	}
	if vo == nil || vo.TimelineOffsetUS != 0 || vo.TimelineDurationUS != 5_000_000 {
		t.Fatalf("voiceover must carry explicit timeline placement: %+v", vo)
	}
	if clip == nil {
		t.Fatalf("clip intent missing: %+v", intents)
	}

	// Clip audio is ducked, never full-volume, with ducking automation.
	clipEvents := eventsForRole(plan, audio.TrackClipAudio)
	if len(clipEvents) != 1 || clipEvents[0].GainDB != audio.DuckClipBaseGainDB {
		t.Fatalf("clip audio must be ducked: %+v", clipEvents)
	}
	if len(plan.Automation) == 0 {
		t.Fatalf("ducking automation missing: %+v", plan.Automation)
	}
}

// TestCompileCanonicalAudioPlanAudioOnlyKeepsDuckedClipAudio certifies the
// audio-only master contract: the COMBINED_TIMELINE
// compile must keep the original clip audio mixed underneath the narration
// (VOICEOVER_DUCKED_CLIP + ducking automation), clamp the clip intent to the
// VO-governed scene window, and strip only the video/source windows — never
// the clip audio itself.
func TestCompileCanonicalAudioPlanAudioOnlyKeepsDuckedClipAudio(t *testing.T) {
	// Scene is VO-governed (8s editorial window) while the clip source is
	// longer (12s): audio-only must clamp the placed clip audio to 8s.
	clip := &ClipReference{ID: "clip-12", AudioPath: "/media/clip-12.mp4", SourceInMS: 1000, SourceOutMS: 13000}
	result := GenerateResult{
		AudioMode: audio.AudioModeCombinedTimeline,
		Scenes: []Scene{{
			ID:         "scene-audio-only",
			Index:      0,
			DurationUS: 8_000_000,
			Clip:       clip,
			Clips:      []*ClipReference{clip},
			Audio:      audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-12", SourceInUS: 1_000_000, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true},
			AudioIntents: []audio.AudioIntent{
				{Mode: audio.AudioClip, ClipAssetID: "clip-12", SourceInUS: 1_000_000, SourceDurationUS: 12_000_000, TimelineOffsetUS: 0, TimelineDurationUS: 12_000_000, UseOriginalAudio: true},
			},
			Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/vo-a.m4a", Duration: 8.0}},
		}},
	}
	timeline, plan, assets, _, err := CompileCanonicalAudioPlanAudioOnly(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}

	// The audio-only master keeps the ducked-clip mix decision.
	if plan.MixPolicy != audio.MixVoiceoverWithDuckedClip {
		t.Fatalf("mix policy = %q, want %q", plan.MixPolicy, audio.MixVoiceoverWithDuckedClip)
	}

	// Video/source windows are excluded, but the clip audio intent survives,
	// clamped to the VO-governed scene window (never the 12s source span).
	seg := timeline.Segments[0]
	if len(seg.EffectiveVideoSegments()) != 0 {
		t.Fatalf("audio-only timeline must not carry video segments: %+v", seg.EffectiveVideoSegments())
	}
	var clipIntent *audio.AudioIntent
	for i := range seg.AudioIntents {
		if seg.AudioIntents[i].Mode == audio.AudioClip {
			clipIntent = &seg.AudioIntents[i]
		}
	}
	if clipIntent == nil {
		t.Fatalf("audio-only timeline must keep the clip audio intent: %+v", seg.AudioIntents)
	}
	if clipIntent.TimelineDurationUS != 8_000_000 || clipIntent.SourceDurationUS != 8_000_000 {
		t.Fatalf("clip intent must be clamped to the 8s scene window: %+v", clipIntent)
	}

	// Clip audio is ducked with dynamic ducking automation and its asset is
	// part of the resolved audio assets.
	clipEvents := eventsForRole(plan, audio.TrackClipAudio)
	if len(clipEvents) != 1 || clipEvents[0].GainDB != audio.DuckClipBaseGainDB || clipEvents[0].DurationUS != 8_000_000 {
		t.Fatalf("clip audio must be ducked across the scene window: %+v", clipEvents)
	}
	if len(plan.Automation) == 0 {
		t.Fatalf("ducking automation missing: %+v", plan.Automation)
	}
	var hasClipAsset bool
	for _, asset := range assets {
		if asset.AssetID == "clip-12" && asset.Path == "/media/clip-12.mp4" {
			hasClipAsset = true
		}
	}
	if !hasClipAsset {
		t.Fatalf("clip audio asset must be resolved for the audio-only master: %+v", assets)
	}
}

func TestCompileCanonicalAudioPlanRepresentsFreezeTailInTimeline(t *testing.T) {
	// A clip shorter than its narration must freeze on its last frame: the
	// canonical timeline carries an explicit synthetic freeze tail instead of
	// leaving the renderer to guess a black gap.
	clip := &ClipReference{ID: "clip-16", AudioPath: "/media/clip-16.m4a", SourceInMS: 0, SourceOutMS: 16000}
	result := GenerateResult{
		AudioMode: audio.AudioModeCombinedTimeline,
		Scenes: []Scene{{
			ID:    "scene-8",
			Index: 0,
			Clip:  clip,
			Clips: []*ClipReference{clip},
			Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
			AudioIntents: []audio.AudioIntent{
				{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
			},
			Voiceover: map[Language]AudioReference{"it": {ID: "vo-8", FilePath: "/media/vo-8.m4a", Duration: 17.52}},
		}},
	}
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if timeline.DurationUS != 17_520_000 {
		t.Fatalf("timeline duration = %dus, want 17520000us", timeline.DurationUS)
	}
	videos := timeline.Segments[0].EffectiveVideoSegments()
	if len(videos) != 2 {
		t.Fatalf("video segments = %d, want 2 (real clip + freeze tail): %+v", len(videos), videos)
	}
	if videos[0].Freeze || videos[0].TimelineDurationUS != 16_000_000 {
		t.Fatalf("real clip segment = %+v, want 16s non-freeze", videos[0])
	}
	freeze := videos[1]
	if !freeze.Freeze || freeze.AssetID != "clip-16" || freeze.TimelineOffsetUS != 16_000_000 || freeze.TimelineDurationUS != 1_520_000 {
		t.Fatalf("freeze tail = %+v, want 1.52s freeze of clip-16 at offset 16s", freeze)
	}
}

func TestCompileCanonicalAudioPlanNoFreezeWhenClipCoversScene(t *testing.T) {
	// Clip 18.8s covers its 17.544s narration: no freeze tail.
	clip := &ClipReference{ID: "clip-18", AudioPath: "/media/clip-18.m4a", SourceInMS: 0, SourceOutMS: 18800}
	result := GenerateResult{
		AudioMode: audio.AudioModeCombinedTimeline,
		Scenes: []Scene{{
			ID:    "scene-0",
			Index: 0,
			Clip:  clip,
			Clips: []*ClipReference{clip},
			Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-18", SourceInUS: 0, SourceDurationUS: 18_800_000, UseOriginalAudio: true},
			AudioIntents: []audio.AudioIntent{
				{Mode: audio.AudioClip, ClipAssetID: "clip-18", SourceInUS: 0, SourceDurationUS: 18_800_000, UseOriginalAudio: true},
			},
			Voiceover: map[Language]AudioReference{"it": {ID: "vo-0", FilePath: "/media/vo-0.m4a", Duration: 17.544}},
		}},
	}
	timeline, _, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	videos := timeline.Segments[0].EffectiveVideoSegments()
	if len(videos) != 1 || videos[0].Freeze {
		t.Fatalf("video segments = %+v, want a single non-freeze clip", videos)
	}
}

func TestValidateVoiceoverSourceDurationsMatchesCertifiedProbe(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-vo", Index: 0, DurationUS: 4_500_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVoiceoverSourceDurations(result, "it", timeline, plan); err != nil {
		t.Fatalf("certified probe 3.2s must match source_duration_us: %v", err)
	}
}

func TestValidateVoiceoverSourceDurationsAllowsWindowClamp(t *testing.T) {
	// Certified file longer than the scene window is legitimately clamped.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-clip", Index: 0, DurationUS: 2_000_000,
		Clip:      &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4"},
		Audio:     audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 0, SourceDurationUS: 2_000_000, UseOriginalAudio: true},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 12.5}},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVoiceoverSourceDurations(result, "it", timeline, plan); err != nil {
		t.Fatalf("clamped source_duration_us must pass certification: %v", err)
	}
}

func TestValidateVoiceoverSourceDurationsLenientWithoutCertifiedProbe(t *testing.T) {
	// No certified probe: the compile window fallback is allowed, and the
	// cert-time check stays lenient too.
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-unprobed", Index: 0, DurationUS: 4_000_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a"}},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVoiceoverSourceDurations(result, "it", timeline, plan); err != nil {
		t.Fatalf("unprobed voiceover must pass lenient certification: %v", err)
	}
}

func TestValidateVoiceoverSourceDurationsFailsClosedOnDrift(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-vo", Index: 0, DurationUS: 4_500_000,
		Audio:     audio.AudioIntent{Mode: audio.AudioVoiceover},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Tracks {
		for j := range plan.Tracks[i].Events {
			if plan.Tracks[i].Events[j].Type == audio.EventVoiceover {
				plan.Tracks[i].Events[j].SourceDurationUS = 4_000_000
			}
		}
	}
	if err := ValidateVoiceoverSourceDurations(result, "it", timeline, plan); err == nil {
		t.Fatal("drifted source_duration_us must fail certification")
	}
}

func TestCompileCanonicalAudioPlanFailsClosedForMissingClipAudio(t *testing.T) {
	_, _, _, err := CompileCanonicalAudioPlan(GenerateResult{Scenes: []Scene{{
		ID: "scene-1", Index: 0, DurationMS: 1000, Clip: &ClipReference{ID: "clip-1"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-1"},
	}}}, "en", audio.DefaultAudioProfile())
	if err == nil {
		t.Fatal("missing clip audio must fail closed")
	}
}

func TestCompileCanonicalAudioPlanPreservesUSDurationAndCombinedIntents(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "comedian-a", Index: 0, DurationUS: 5_600_000,
		Clip:      &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4", SourceInMS: 33200, SourceOutMS: 38800},
		Audio:     audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 33_200_000, SourceDurationUS: 5_600_000, UseOriginalAudio: true},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/a.m4a"}},
	}}}
	timeline, plan, assets, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Segments[0].DurationUS != 5_600_000 || len(timeline.Segments[0].AudioIntents) != 2 {
		t.Fatalf("resolved scene timing/intents lost: %#v", timeline.Segments[0])
	}
	if len(plan.Tracks) != 2 || len(plan.Tracks[0].Events) != 1 || len(plan.Tracks[1].Events) != 1 {
		t.Fatalf("combined plan not multi-track: %#v", plan.Tracks)
	}
	if len(assets) != 2 {
		t.Fatalf("expected clip and voiceover assets, got %#v", assets)
	}
}

func TestCompileCanonicalAudioPlanUsesEveryClipOwnedByScene(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-multi", Index: 0,
		Clips: []*ClipReference{
			{ID: "clip-a", AudioPath: "/video/a.mp4", SourceOutMS: 2000, Duration: 2},
			{ID: "clip-b", AudioPath: "/video/b.mp4", SourceOutMS: 3000, Duration: 3},
		},
		Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceDurationUS: 2000000},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceDurationUS: 2000000, TimelineDurationUS: 2000000, UseOriginalAudio: true},
			{Mode: audio.AudioClip, ClipAssetID: "clip-b", SourceDurationUS: 3000000, TimelineOffsetUS: 2000000, TimelineDurationUS: 3000000, UseOriginalAudio: true},
		},
	}}}
	timeline, _, assets, err := CompileCanonicalAudioPlan(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	videos := timeline.Segments[0].EffectiveVideoSegments()
	if len(videos) != 2 || videos[0].AssetID != "clip-a" || videos[1].AssetID != "clip-b" || videos[1].TimelineOffsetUS != 2000000 {
		t.Fatalf("multi-clip video projection = %+v", videos)
	}
	if len(assets) != 2 {
		t.Fatalf("multi-clip audio assets = %+v, want both clip assets", assets)
	}
}

func TestValidateChunkedVoiceoversRequiresOneToOneMapping(t *testing.T) {
	base := GenerateResult{Scenes: []Scene{
		{ID: "scene-1", Index: 0, Text: map[Language]string{"en": "hello"}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1", FilePath: "/vo-1.mp3"}}},
		{ID: "scene-2", Index: 1, Text: map[Language]string{"en": "world"}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-2", FilePath: "/vo-2.mp3"}}},
	}}
	if err := ValidateChunkedVoiceovers(base); err != nil {
		t.Fatal(err)
	}
	base.Scenes[1].Voiceover["en"] = base.Scenes[0].Voiceover["en"]
	if err := ValidateChunkedVoiceovers(base); err == nil {
		t.Fatal("duplicate voiceover mapping must fail")
	}
	delete(base.Scenes[1].Voiceover, "en")
	if err := ValidateChunkedVoiceovers(base); err == nil {
		t.Fatal("missing voiceover mapping must fail")
	}
}

// TestCompileCanonicalAudioPlanWithTimingsMatchesCanonicalSpelling certifies
// the timing-insensitive spelling delegates to the timing variant and returns
// identical artifacts — the refactor must not change the compiled
// timeline/plan/assets.
func TestCompileCanonicalAudioPlanWithTimingsMatchesCanonicalSpelling(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{
		{ID: "scene-001", Index: 0, DurationMS: 14000, Audio: audio.AudioIntent{Mode: audio.AudioVoiceover}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1", FilePath: "/audio/vo-1.mp3"}}},
		{ID: "scene-002", Index: 1, DurationMS: 12000, Clip: &ClipReference{ID: "clip-1", AudioPath: "/video/clip-1.mp4"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-1", SourceInUS: 34000000, SourceDurationUS: 12000000}},
		{ID: "scene-003", Index: 2, DurationMS: 2000, Audio: audio.AudioIntent{Mode: audio.AudioSilence}},
	}}

	plainTimeline, plainPlan, plainAssets, err := CompileCanonicalAudioPlan(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	timedTimeline, timedPlan, timedAssets, timings, err := CompileCanonicalAudioPlanWithTimings(result, "en", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}

	require.JSONEq(t, mustJSON(t, plainTimeline), mustJSON(t, timedTimeline))
	require.JSONEq(t, mustJSON(t, plainPlan), mustJSON(t, timedPlan))
	require.JSONEq(t, mustJSON(t, plainAssets), mustJSON(t, timedAssets))

	if timings.TimelineCompileMS < 0 || timings.ClipAudioPrepareMS < 0 || timings.AudioPlanCompileMS < 0 {
		t.Fatalf("compile timings must be non-negative: %+v", timings)
	}
}

// masterAudioRef builds a final-audio reference whose duration exactly matches
// the compiled plan (perfect deterministic render) so the master invariants
// pass the encoder-padding tolerance.
func masterAudioRef(plan audio.CompiledAudioPlan) FinalAudioReference {
	return FinalAudioReference{DurationUS: plan.DurationUS, DurationMS: plan.DurationUS / 1000}
}

// narrationDrivenResult builds N narration-driven scenes: each scene carries
// only a voiceover intent and its duration equals the voiceover duration, so
// the canonical timeline is driven solely by the voiceovers.
func narrationDrivenResult() GenerateResult {
	return GenerateResult{Scenes: []Scene{
		{ID: "scene-0", Index: 0, DurationUS: 4_500_000, Audio: audio.AudioIntent{Mode: audio.AudioVoiceover}, Voiceover: map[Language]AudioReference{"it": {ID: "vo-0", FilePath: "/audio/vo-0.m4a", Duration: 4.5}}},
		{ID: "scene-1", Index: 1, DurationUS: 3_200_000, Audio: audio.AudioIntent{Mode: audio.AudioVoiceover}, Voiceover: map[Language]AudioReference{"it": {ID: "vo-1", FilePath: "/audio/vo-1.m4a", Duration: 3.2}}},
	}}
}

func TestValidateMasterAudioInvariantsNarrationDrivenTiles(t *testing.T) {
	timeline, plan, _, err := CompileCanonicalAudioPlan(narrationDrivenResult(), "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMasterAudioInvariants(timeline, plan, masterAudioRef(plan)); err != nil {
		t.Fatalf("narration-driven master must satisfy invariants: %v", err)
	}
}

func TestValidateMasterAudioInvariantsPlanDurationDriftFails(t *testing.T) {
	timeline, plan, _, err := CompileCanonicalAudioPlan(narrationDrivenResult(), "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	plan.DurationUS = timeline.DurationUS + 1
	if err := ValidateMasterAudioInvariants(timeline, plan, masterAudioRef(plan)); err == nil {
		t.Fatal("plan/timeline duration drift must fail closed")
	}
}

func TestValidateMasterAudioInvariantsVoiceoverGapFails(t *testing.T) {
	timeline, plan, _, err := CompileCanonicalAudioPlan(narrationDrivenResult(), "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	// Shorten the first voiceover event so it no longer covers its scene:
	// the narration track now leaves a gap and must fail closed.
	for i := range plan.Tracks {
		for j := range plan.Tracks[i].Events {
			if plan.Tracks[i].Events[j].Type == audio.EventVoiceover {
				plan.Tracks[i].Events[j].DurationUS = 1
				break
			}
		}
	}
	if err := ValidateMasterAudioInvariants(timeline, plan, masterAudioRef(plan)); err == nil {
		t.Fatal("voiceover track gap must fail closed")
	}
}

func TestValidateMasterAudioInvariantsFinalAudioOutOfToleranceFails(t *testing.T) {
	timeline, plan, _, err := CompileCanonicalAudioPlan(narrationDrivenResult(), "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	ref := masterAudioRef(plan)
	ref.DurationUS = timeline.DurationUS + FinalAudioDurationToleranceUS + 1
	if err := ValidateMasterAudioInvariants(timeline, plan, ref); err == nil {
		t.Fatal("final_audio outside the tolerance must fail closed")
	}
}

func TestValidateMasterAudioInvariantsClipDrivenSkipsTiling(t *testing.T) {
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-clip", Index: 0, DurationUS: 5_600_000,
		Clip:  &ClipReference{ID: "clip-a", AudioPath: "/video/a.mp4"},
		Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 0, SourceDurationUS: 5_600_000, UseOriginalAudio: true},
	}}}
	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	// A clip-driven master has no narration tiling requirement: the clip track
	// owns the coverage, so the master invariant must pass without a VO track.
	if err := ValidateMasterAudioInvariants(timeline, plan, masterAudioRef(plan)); err != nil {
		t.Fatalf("clip-driven master must satisfy invariants: %v", err)
	}
}
