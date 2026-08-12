package audio

import (
	"encoding/json"
	"errors"
	"testing"
)

func testTimeline() CanonicalTimeline {
	return CanonicalTimeline{Version: TimelineVersion, DurationUS: 60000000, Segments: []TimelineSegment{
		{ID: "s1", Index: 0, TimelineStartUS: 0, DurationUS: 14000000, Audio: AudioIntent{Mode: AudioVoiceover, VoiceoverAssetID: "vo_001"}},
		{ID: "s2", Index: 1, TimelineStartUS: 14000000, DurationUS: 12000000, Audio: AudioIntent{Mode: AudioClip, ClipAssetID: "clip_001", SourceInUS: 34000000, SourceDurationUS: 12000000}},
		{ID: "s3", Index: 2, TimelineStartUS: 26000000, DurationUS: 18000000, Audio: AudioIntent{Mode: AudioVoiceover, VoiceoverAssetID: "vo_002"}},
		{ID: "s4", Index: 3, TimelineStartUS: 44000000, DurationUS: 14000000, Audio: AudioIntent{Mode: AudioClip, ClipAssetID: "clip_002", SourceInUS: 10000000, SourceDurationUS: 14000000}},
		{ID: "s5", Index: 4, TimelineStartUS: 58000000, DurationUS: 2000000, Audio: AudioIntent{Mode: AudioSilence}},
	}}
}

func TestCompileUsesCanonicalTimingAndSealsDeterministically(t *testing.T) {
	timeline := testTimeline()
	plan, err := Compile(timeline, DefaultAudioProfile())
	if err != nil {
		t.Fatal(err)
	}
	if plan.DurationUS != timeline.DurationUS || len(plan.Events) != len(timeline.Segments) {
		t.Fatalf("plan timing mismatch: %+v", plan)
	}
	if plan.Events[1].TimelineStartUS != 14000000 || plan.Events[1].SourceInUS != 34000000 || plan.Events[1].SourceDurationUS != 12000000 {
		t.Fatalf("clip source/timeline timing lost: %+v", plan.Events[1])
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
	for _, key := range []string{"audio_plan_version", "timeline_version", "duration_us", "primary_events", "canonical_audio_profile", "audio_plan_sha256"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("compiled plan missing canonical JSON field %q: %s", key, encoded)
		}
	}
	if got := plan.Events[0].SourceDurationUS; got != 14000000 {
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
	for i, want := range wantStarts {
		if plan.Events[i].TimelineStartUS != want*1000 {
			t.Fatalf("audio event %d start = %d, want %d", i, plan.Events[i].TimelineStartUS/1000, want)
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
	if plan.Events[2].TimelineStartUS != 29000000 || plan.Events[3].TimelineStartUS != 47000000 || plan.Events[4].TimelineStartUS != 61000000 {
		t.Fatalf("audio offsets did not follow canonical timeline: %+v", plan.Events)
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
		requested         AudioMode
		voiceover, render bool
		want              AudioMode
		wantErr           bool
	}{
		{requested: "", voiceover: false, render: false, want: AudioModeNone},
		{requested: "", voiceover: true, render: true, wantErr: true},
		{requested: "combined_timeline", voiceover: true, render: true, want: AudioModeCombinedTimeline},
		{requested: "bogus", render: true, wantErr: true},
		{requested: AudioModeCombinedTimeline, render: false, wantErr: true},
	}
	for _, tc := range cases {
		got, err := ResolveAudioMode(tc.requested, tc.voiceover, tc.render)
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
