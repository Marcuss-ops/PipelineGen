package scriptgeneration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
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
	resolved, err := ResolveScenes(result.Scenes, "it")
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

// TestFinalAudioCanonicalTimelineAndRenderPlanShareOneAsset certifies the
// three audio-side artifacts of a generation agree on a single master asset:
//
//	final_audio.m4a   (GenerateResult.FinalAudio)
//	CanonicalTimeline (the timing SSOT the mix was compiled from)
//	RenderPlan.FinalAudio (the asset the video executor must mux in)
//
// The link is the audio_asset_id plus the audio_plan_sha256 chain: the
// compiled plan hash is derived from the canonical timeline, stamped onto the
// certified final audio file, and carried verbatim into the render plan so the
// renderer consumes exactly the same asset (never a re-generated copy).
func TestFinalAudioCanonicalTimelineAndRenderPlanShareOneAsset(t *testing.T) {
	clipSHA := strings.Repeat("a", 64)
	result := GenerateResult{Scenes: []Scene{{
		ID: "scene-combined", Index: 0, DurationUS: 5_000_000,
		Clip:      &ClipReference{ID: "clip-a", Path: "/video/a.mp4", SHA256: clipSHA, AudioPath: "/video/a.mp4", SourceInMS: 0, SourceOutMS: 5000, Duration: 5},
		Audio:     audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-a", SourceInUS: 0, SourceDurationUS: 5_000_000, UseOriginalAudio: true},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-a", FilePath: "/audio/a.m4a", Duration: 4.0}},
	}}}

	timeline, plan, _, err := CompileCanonicalAudioPlan(result, "it", audio.DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}

	// The combined-audio renderer certifies the master file (final_audio.m4a)
	// against the compiled plan derived from the canonical timeline.
	ref := FinalAudioReference{
		AssetID:              "final-audio-it-abc",
		Path:                 "/tmp/final_audio_it.m4a",
		AudioContractVersion: audio.AudioContractVersion,
		AudioPlanVersion:     plan.Version,
		PlanSHA256:           plan.PlanSHA256,
		FinalAudioSHA256:     strings.Repeat("0", 64),
		Codec:                plan.Output.Codec,
		Profile:              plan.Output.Profile,
		SampleRate:           plan.Output.SampleRate,
		Channels:             plan.Output.Channels,
		ChannelLayout:        plan.Output.ChannelLayout,
		Bitrate:              128000,
		DurationMS:           plan.DurationUS / 1000,
		StartPTS:             0,
		SizeBytes:            1,
		FinalMix:             true,
		CopyEligible:         true,
	}
	if err := ValidateFinalAudioReference(ref, plan); err != nil {
		t.Fatalf("final_audio.m4a must certify against the canonical plan: %v", err)
	}
	result.FinalAudio = &ref

	renderPlan, err := CompileCanonicalRenderPlan(result, timeline, "job-1", "rev-1", 30)
	if err != nil {
		t.Fatal(err)
	}
	if renderPlan.FinalAudio == nil {
		t.Fatal("render plan must carry the final audio asset")
	}

	// 1) final_audio.m4a and RenderPlan.FinalAudio share one audio_asset_id.
	if renderPlan.FinalAudio.AssetID != ref.AssetID {
		t.Fatalf("render plan final audio asset_id=%q, want %q (final_audio.m4a)", renderPlan.FinalAudio.AssetID, ref.AssetID)
	}

	// 2) The render plan's final audio is the very same master: same plan
	//    hash, same file hash, same path.
	if renderPlan.FinalAudio.PlanSHA256 != plan.PlanSHA256 || renderPlan.FinalAudio.SHA256 != ref.FinalAudioSHA256 || renderPlan.FinalAudio.Path != ref.Path {
		t.Fatalf("render plan final audio diverges from final_audio.m4a: plan_sha256=%q file_sha256=%q path=%q", renderPlan.FinalAudio.PlanSHA256, renderPlan.FinalAudio.SHA256, renderPlan.FinalAudio.Path)
	}

	// 3) The plan hash recorded on final_audio.m4a is derived from this exact
	//    canonical timeline (not some other timeline or a re-mixed copy).
	if ref.PlanSHA256 != plan.PlanSHA256 {
		t.Fatalf("final_audio.m4a plan_sha256=%q, want %q", ref.PlanSHA256, plan.PlanSHA256)
	}

	// 4) The render plan embeds the same canonical timeline the audio plan was
	//    compiled from (verified by the sealed timeline hash).
	timelineHash, err := timeline.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if renderPlan.TimelineHash != timelineHash {
		t.Fatalf("render plan timeline hash=%q, want canonical %q", renderPlan.TimelineHash, timelineHash)
	}
}

// TestValidateFinalAudioMirrorEnforcesSameAsset certifies the fails-closed
// invariant that ties the three audio-side artifacts to a single master:
// RenderPlan.FinalAudio must mirror the certified FinalAudioReference field
// for field — most importantly the audio_asset_id, the final-audio file hash,
// and the audio-plan hash (the latter two binding it to the canonical
// timeline it was compiled from). Any divergence must fail closed rather
// than letting the video executor consume a different file.
func TestValidateFinalAudioMirrorEnforcesSameAsset(t *testing.T) {
	ref := FinalAudioReference{
		AssetID: "final-audio-abc", Path: "/tmp/final_audio_abc.m4a",
		AudioContractVersion: audio.AudioContractVersion,
		AudioPlanVersion:     audio.AudioPlanVersion,
		PlanSHA256:           strings.Repeat("a", 64),
		FinalAudioSHA256:     strings.Repeat("b", 64),
		Codec:                "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
		Bitrate: 128000, DurationMS: 5000, StartPTS: 0, SizeBytes: 1, FinalMix: true, CopyEligible: true,
	}
	mirror := func() render.FinalAudioAsset {
		return render.FinalAudioAsset{
			AssetID: ref.AssetID, AssetKind: "final_audio", Strategy: string(audio.FinalAudioCopy),
			Path: ref.Path, SHA256: ref.FinalAudioSHA256, PlanSHA256: ref.PlanSHA256,
			AudioContractVersion: ref.AudioContractVersion, AudioPlanVersion: ref.AudioPlanVersion,
			Codec: ref.Codec, Profile: ref.Profile, SampleRate: ref.SampleRate, Channels: ref.Channels,
			ChannelLayout: ref.ChannelLayout, DurationMS: ref.DurationMS, StartPTS: ref.StartPTS,
			SizeBytes: ref.SizeBytes, FinalMix: ref.FinalMix, CopyEligible: ref.CopyEligible,
		}
	}

	// A faithful mirror passes.
	if err := ValidateFinalAudioMirror(ref, mirror()); err != nil {
		t.Fatalf("matching mirror must pass: %v", err)
	}

	// A diverged audio_asset_id must fail closed.
	tampered := mirror()
	tampered.AssetID = "final-audio-other"
	if err := ValidateFinalAudioMirror(ref, tampered); err == nil {
		t.Fatal("diverged audio_asset_id must fail closed")
	}

	// A diverged final-audio file hash must fail closed.
	tampered = mirror()
	tampered.SHA256 = strings.Repeat("c", 64)
	if err := ValidateFinalAudioMirror(ref, tampered); err == nil {
		t.Fatal("diverged final-audio hash must fail closed")
	}

	// A diverged audio-plan hash (i.e. a different canonical timeline) must
	// fail closed.
	tampered = mirror()
	tampered.PlanSHA256 = strings.Repeat("d", 64)
	if err := ValidateFinalAudioMirror(ref, tampered); err == nil {
		t.Fatal("diverged audio-plan hash must fail closed")
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

func TestCompileCanonicalRenderPlanMirrorsCertifiedFinalAudio(t *testing.T) {
	result := GenerateResult{
		Scenes: []Scene{{ID: "scene-1", Index: 0, DurationUS: 1_000_000}},
		FinalAudio: &FinalAudioReference{
			AssetID: "final-audio-1", Path: "/audio/final_audio.m4a",
			AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.AudioPlanVersion,
			PlanSHA256:       "plan",
			FinalAudioSHA256: strings.Repeat("a", 64),
			Codec:            "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
			DurationMS: 1000, StartPTS: 0, SizeBytes: 123, FinalMix: true, CopyEligible: true,
		},
	}
	timeline, err := CompileCanonicalTimeline(result)
	if err != nil {
		t.Fatal(err)
	}
	renderPlan, err := CompileCanonicalRenderPlanWithFrameRate(result, timeline, "job-1", "rev-1", audio.IntegerFrameRate(30))
	if err != nil {
		t.Fatal(err)
	}
	if renderPlan.FinalAudio == nil {
		t.Fatal("render plan must carry final audio")
	}
	if err := ValidateFinalAudioMirror(*result.FinalAudio, *renderPlan.FinalAudio); err != nil {
		t.Fatalf("render plan must mirror the certified final audio: %v", err)
	}
}

func TestValidateFinalAudioMirrorFailsClosedOnDrift(t *testing.T) {
	ref := FinalAudioReference{
		AssetID: "final-audio-1", Path: "/audio/final_audio.m4a",
		AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.AudioPlanVersion,
		PlanSHA256:       "plan",
		FinalAudioSHA256: strings.Repeat("b", 64),
		Codec:            "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
		DurationMS: 1000, StartPTS: 0, SizeBytes: 123, FinalMix: true, CopyEligible: true,
	}
	good := render.FinalAudioAsset{
		AssetID: ref.AssetID, AssetKind: "final_audio", Strategy: string(audio.FinalAudioCopy),
		Path: ref.Path, SHA256: ref.FinalAudioSHA256, PlanSHA256: ref.PlanSHA256,
		AudioContractVersion: ref.AudioContractVersion, AudioPlanVersion: ref.AudioPlanVersion,
		Codec: ref.Codec, Profile: ref.Profile, SampleRate: ref.SampleRate, Channels: ref.Channels, ChannelLayout: ref.ChannelLayout,
		DurationMS: ref.DurationMS, StartPTS: ref.StartPTS, SizeBytes: ref.SizeBytes, FinalMix: ref.FinalMix, CopyEligible: ref.CopyEligible,
	}
	if err := ValidateFinalAudioMirror(ref, good); err != nil {
		t.Fatalf("faithful mirror must pass: %v", err)
	}
	good.SHA256 = strings.Repeat("c", 64)
	if err := ValidateFinalAudioMirror(ref, good); err == nil {
		t.Fatal("drifted SHA256 must fail closed")
	}
}
