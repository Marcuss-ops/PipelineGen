package audio

import (
	"encoding/json"
	"errors"
	"testing"
)

func eventsForRole(plan CompiledAudioPlan, role AudioTrackRole) []AudioEvent {
	for _, track := range plan.Tracks {
		if track.Role == role {
			return track.Events
		}
	}
	return nil
}

func testTimeline() CanonicalTimeline {
	return CanonicalTimeline{Version: TimelineVersion, DurationUS: 60000000, Segments: []TimelineSegment{
		{ID: "s1", Index: 0, TimelineStartUS: 0, DurationUS: 14000000, Audio: AudioIntent{Mode: AudioVoiceover, VoiceoverAssetID: "vo_001"}},
		{ID: "s2", Index: 1, TimelineStartUS: 14000000, DurationUS: 12000000, Audio: AudioIntent{Mode: AudioClip, ClipAssetID: "clip_001", SourceInUS: 34000000, SourceDurationUS: 12000000}},
		{ID: "s3", Index: 2, TimelineStartUS: 26000000, DurationUS: 18000000, Audio: AudioIntent{Mode: AudioVoiceover, VoiceoverAssetID: "vo_002"}},
		{ID: "s4", Index: 3, TimelineStartUS: 44000000, DurationUS: 14000000, Audio: AudioIntent{Mode: AudioClip, ClipAssetID: "clip_002", SourceInUS: 10000000, SourceDurationUS: 14000000}},
		{ID: "s5", Index: 4, TimelineStartUS: 58000000, DurationUS: 2000000, Audio: AudioIntent{Mode: AudioSilence}},
	}}
}

func TestDefaultAudioProfileUsesAssemblyV2(t *testing.T) {
	profile := DefaultAudioProfile()
	if profile.Codec != "aac" || profile.Profile != "LC" || profile.SampleRate != 48000 || profile.Channels != 2 || profile.ChannelLayout != "stereo" || profile.Bitrate != "192k" {
		t.Fatalf("unexpected canonical audio profile: %+v", profile)
	}
}

func TestCompileUsesCanonicalTimingAndSealsDeterministically(t *testing.T) {
	timeline := testTimeline()
	plan, err := Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if plan.DurationUS != timeline.DurationUS || len(eventsForRole(plan, TrackVoiceover))+len(eventsForRole(plan, TrackClipAudio)) != len(timeline.Segments) {
		t.Fatalf("plan timing mismatch: %+v", plan)
	}
	clipEvents := eventsForRole(plan, TrackClipAudio)
	if clipEvents[0].TimelineStartUS != 14000000 || clipEvents[0].SourceInUS != 34000000 || clipEvents[0].SourceDurationUS != 12000000 {
		t.Fatalf("clip source/timeline timing lost: %+v", clipEvents[0])
	}
	if plan.PlanSHA256 == "" {
		t.Fatal("plan hash is empty")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"audio_plan_version", "timeline_version", "duration_us", "tracks", "canonical_audio_profile", "audio_plan_sha256"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("compiled plan missing canonical JSON field %q: %s", key, encoded)
		}
	}
	if got := eventsForRole(plan, TrackVoiceover)[0].SourceDurationUS; got != 14000000 {
		t.Fatalf("voiceover source range = %d, want 14000", got)
	}
	for i := 0; i < 100; i++ {
		second, err := Compile(timeline, DefaultAudioProfile())
		if err != nil {
			t.Fatal(err)
		}
		if plan.PlanSHA256 != second.PlanSHA256 {
			t.Fatalf("hash is not deterministic at iteration %d: %s != %s", i, plan.PlanSHA256, second.PlanSHA256)
		}
	}
}

func TestCanonicalTimelineMatchesVideoAndAudioOffsets(t *testing.T) {
	timeline := testTimeline()
	if timeline.DurationUS != 60000000 {
		t.Fatalf("duration = %d, want 60000", timeline.DurationUS/1000)
	}
	wantStarts := []int64{0, 14000, 26000, 44000, 58000}
	for i, want := range wantStarts {
		if timeline.Segments[i].TimelineStartUS != want*1000 {
			t.Fatalf("segment %d start = %d, want %d", i, timeline.Segments[i].TimelineStartUS/1000, want)
		}
	}
	plan, err := Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range wantStarts {
		found := false
		for _, event := range append(eventsForRole(plan, TrackVoiceover), eventsForRole(plan, TrackClipAudio)...) {
			if event.TimelineStartUS == want*1000 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("audio event start %d not found", want)
		}
	}
	// A duration change is applied once to the canonical timeline; all later
	// offsets move together instead of being independently recomputed.
	timeline.Segments[1].DurationUS = 15000000
	timeline.Segments[2].TimelineStartUS = 29000000
	timeline.Segments[3].TimelineStartUS = 47000000
	timeline.Segments[4].TimelineStartUS = 61000000
	timeline.DurationUS = 63000000
	plan, err = Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	starts := map[int64]bool{}
	for _, event := range append(eventsForRole(plan, TrackVoiceover), eventsForRole(plan, TrackClipAudio)...) {
		starts[event.TimelineStartUS] = true
	}
	for _, want := range []int64{29000000, 47000000, 61000000} {
		if !starts[want] {
			t.Fatalf("audio offset %d did not follow canonical timeline: %+v", want, starts)
		}
	}
}

func TestCompileRejectsInvalidAudioInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CanonicalTimeline)
	}{
		{"negative start", func(v *CanonicalTimeline) { v.Segments[1].TimelineStartUS = -1 }},
		{"non-positive duration", func(v *CanonicalTimeline) { v.Segments[1].DurationUS = 0 }},
		{"missing voiceover asset", func(v *CanonicalTimeline) { v.Segments[0].Audio.VoiceoverAssetID = "" }},
		{"missing clip asset", func(v *CanonicalTimeline) { v.Segments[1].Audio.ClipAssetID = "" }},
		{"invalid source range", func(v *CanonicalTimeline) { v.Segments[1].Audio.SourceDurationUS = -1 }},
		{"event past timeline", func(v *CanonicalTimeline) { v.Segments[4].DurationUS = 3000000; v.DurationUS = 60000000 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timeline := testTimeline()
			tc.mutate(&timeline)
			if _, err := Compile(timeline, DefaultAudioProfile()); err == nil {
				t.Fatal("expected fail-closed validation error")
			}
		})
	}
}

func TestPlanHashChangesWhenAudioContentChanges(t *testing.T) {
	base, err := Compile(testTimeline(), DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*CanonicalTimeline){
		func(v *CanonicalTimeline) { v.Segments[0].Audio.GainDB = -3 },
		func(v *CanonicalTimeline) { v.Segments[1].Audio.SourceInUS++ },
		func(v *CanonicalTimeline) { v.Segments[1].Audio.ClipAssetID = "other" },
	}
	for i, mutate := range mutations {
		timeline := testTimeline()
		mutate(&timeline)
		changed, err := Compile(timeline, DefaultAudioProfile())
		if err != nil {
			t.Fatal(err)
		}
		if changed.PlanSHA256 == base.PlanSHA256 {
			t.Fatalf("mutation %d did not change plan hash", i)
		}
	}
}

func TestCompileV2AllowsVoiceoverAndClipAudioOnSameScene(t *testing.T) {
	timeline := CanonicalTimeline{
		Version: TimelineVersion, DurationUS: 5_000_000,
		Segments: []TimelineSegment{{
			ID: "scene-1", Index: 0, TimelineStartUS: 0, DurationUS: 5_000_000,
			Video: VideoSegment{AssetID: "clip-1", SourceInUS: 33_200_000, SourceDurationUS: 5_000_000},
			AudioIntents: []AudioIntent{
				{Mode: AudioVoiceover, VoiceoverAssetID: "vo-1"},
				{Mode: AudioClip, ClipAssetID: "clip-1", SourceInUS: 33_200_000, SourceDurationUS: 5_000_000},
			},
		}},
	}
	plan, err := Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != AudioPlanVersion || len(plan.Tracks) != 2 {
		t.Fatalf("expected v2 with two tracks, got version=%s tracks=%d", plan.Version, len(plan.Tracks))
	}
	if len(plan.Tracks[0].Events) != 1 || len(plan.Tracks[1].Events) != 1 {
		t.Fatalf("expected one event per primary track: %#v", plan.Tracks)
	}
	if plan.Tracks[0].Role != TrackVoiceover || plan.Tracks[1].Role != TrackClipAudio {
		t.Fatalf("unexpected track roles: %#v", plan.Tracks)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["primary_events"]; ok {
		t.Fatal("v2 must not serialize primary_events")
	}
	if _, ok := wire["background_music"]; ok {
		t.Fatal("v2 must not serialize legacy background_music")
	}
	if _, ok := wire["sfx"]; ok {
		t.Fatal("v2 must not serialize legacy sfx")
	}
}

func TestV2RejectsDuplicateEventID(t *testing.T) {
	timeline := CanonicalTimeline{Version: TimelineVersion, DurationUS: 2_000_000, Segments: []TimelineSegment{
		{ID: "a", Index: 0, TimelineStartUS: 0, DurationUS: 1_000_000, AudioIntents: []AudioIntent{{Mode: AudioVoiceover, VoiceoverAssetID: "vo-a"}}},
		{ID: "b", Index: 1, TimelineStartUS: 1_000_000, DurationUS: 1_000_000, AudioIntents: []AudioIntent{{Mode: AudioVoiceover, VoiceoverAssetID: "vo-b"}}},
	}}
	plan, err := Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	plan.Tracks[0].Events[1].EventID = plan.Tracks[0].Events[0].EventID
	if err := plan.Validate(); err == nil {
		t.Fatal("duplicate event IDs must be rejected")
	}
}

func TestCanonicalTimelineJSONCarriesPerSceneTimingWithoutEndUS(t *testing.T) {
	timeline := testTimeline()
	encoded, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Segments []struct {
			ID              string `json:"id"`
			TimelineStartUS int64  `json:"timeline_start_us"`
			DurationUS      int64  `json:"duration_us"`
			EndUS           *int64 `json:"end_us"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Segments) != len(timeline.Segments) {
		t.Fatalf("wire segments = %d, want %d", len(wire.Segments), len(timeline.Segments))
	}
	var expectedEnd int64
	for i, seg := range wire.Segments {
		if seg.TimelineStartUS != timeline.Segments[i].TimelineStartUS || seg.DurationUS != timeline.Segments[i].DurationUS {
			t.Fatalf("segment %d timing not preserved on the wire: %+v", i, seg)
		}
		if seg.EndUS != nil {
			t.Fatalf("segment %d must not serialize end_us; end is derived as start + duration", i)
		}
		if seg.TimelineStartUS != expectedEnd {
			t.Fatalf("segment %d start = %d, want contiguous %d", i, seg.TimelineStartUS, expectedEnd)
		}
		expectedEnd = seg.TimelineStartUS + seg.DurationUS
	}
}

func TestCanonicalTimelineRejectsIndependentTiming(t *testing.T) {
	timeline := testTimeline()
	timeline.Segments[2].TimelineStartUS = 25000000
	if err := timeline.Validate(); err == nil {
		t.Fatal("expected non-contiguous timeline to fail")
	}
}

func TestResolveStrategyFailsClosedForInvalidFinalAudio(t *testing.T) {
	plan, err := Compile(testTimeline(), DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	asset := FinalAudioAsset{AudioContractVersion: AudioContractVersion, AudioPlanVersion: plan.Version, AudioPlanSHA256: plan.PlanSHA256, Bitrate: 128000, FinalMix: true, CopyEligible: true}
	if _, err := ResolveAudioRenderStrategy(&asset, plan); err == nil {
		t.Fatal("invalid final audio must not silently fall back to timeline mix")
	} else if !errors.Is(err, ErrAudioMediaIncompatible) {
		t.Fatalf("error = %v, want AUDIO_MEDIA_INCOMPATIBLE", err)
	}
	strategy, err := ResolveAudioRenderStrategy(nil, plan)
	if err != nil || strategy != TimelineMix {
		t.Fatalf("legacy strategy = %q, %v", strategy, err)
	}
}

func TestResolveAudioModeIsExplicitAndFailClosed(t *testing.T) {
	cases := []struct {
		requested AudioMode
		voiceover bool
		want      AudioMode
		wantErr   bool
	}{
		{requested: "", voiceover: false, want: AudioModeNone},
		{requested: "", voiceover: true, wantErr: true},
		{requested: "combined_timeline", voiceover: true, want: AudioModeCombinedTimeline},
		{requested: "bogus", wantErr: true},
		{requested: AudioModeCombinedTimeline, want: AudioModeCombinedTimeline},
	}
	for _, tc := range cases {
		got, err := ResolveAudioMode(tc.requested, tc.voiceover)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ResolveAudioMode(%q) expected error", tc.requested)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("ResolveAudioMode(%q) = %q, %v; want %q", tc.requested, got, err, tc.want)
		}
	}
}

// TestResolveAudioMode_CombinedTimelineAllowedWithoutVideo certifies the
// semantic separation: COMBINED_TIMELINE compiles a certified
// final_audio.m4a and requires only audio/timeline prerequisites. It must
// be a valid mode even when RenderVideo=false — video rendering is an
// independent flag that never gates the audio master.
func TestResolveAudioMode_CombinedTimelineAllowedWithoutVideo(t *testing.T) {
	got, err := ResolveAudioMode(AudioModeCombinedTimeline, true)
	if err != nil {
		t.Fatalf("COMBINED_TIMELINE must be valid with renderVideo=false: %v", err)
	}
	if got != AudioModeCombinedTimeline {
		t.Fatalf("mode = %q, want %q", got, AudioModeCombinedTimeline)
	}
}
