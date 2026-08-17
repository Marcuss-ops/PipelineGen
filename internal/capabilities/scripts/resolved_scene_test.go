package scriptgeneration

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestResolvedScenesRoundTripPreservesCanonicalTimingAndAssets(t *testing.T) {
	scenes := make([]Scene, 13)
	clipIDs := []string{"11MRzjKA3o7OZGmYZGJMPTGxM_eNt6qAX", "11_5vtQgxOfFdBnsC8FQnCu0UQ2WpfO-c", "128AEiKFTwEZJO4dBUbhe4hu7bzbLAR-W", "1jDRQz8zDFjg86RpgSuxUIHuIE-DKtuET", "1HX3_LUx4Yg-mLaQkukKPrKTlEibuPz98", "1w_wGC43vY4wGtoBOnCErB_r-Hls_bdtO", "1dRv4WFUcXgLUf3QvqheZ9pCqvJY3KcFu", "1Pwng-iqQAVS5VZJmVNGHMBLQBFLsNyOU", "1xWrSFJSg5K5D_hZTxKILhnddvFytWaHI", "1qo69v-Kwuouxyr38-rdsqbS1G53exsFW", "1rsxgdS2fOnIfMAT9LaMpy7U6o07oHtPT", "1teqci7OK3ejVaY6wRGhoWAkDcvHAnIOr", "1tyLaLP_ARFfvaBkEhmNgtg8hLXoGHLsD"}
	for i, clipID := range clipIDs {
		scenes[i] = Scene{ID: "scene-" + string(rune('0'+i)), Index: i, DurationUS: int64(3+i) * 1_000_000, Clip: &ClipReference{ID: clipID, SourceInMS: int64(i+1) * 1000, SourceOutMS: int64(i+4) * 1000, AudioPath: "/media/" + clipID + ".m4a"}, Audio: audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: clipID, SourceInUS: int64(i+1) * 1_000_000, SourceDurationUS: 3_000_000, UseOriginalAudio: true}, Voiceover: map[Language]AudioReference{"it": {ID: "vo-" + string(rune('a'+i)), FilePath: "/voiceover/" + clipID + ".m4a", Duration: float64(2 + i%2)}}}
	}
	before, err := ResolveScenes(scenes, "it", audio.AudioModeNone, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after []ResolvedScene
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("resolved scenes changed after persistence\nbefore=%s\nafter=%s", raw, mustJSON(t, after))
	}
	for _, scene := range after {
		if scene.DurationUS <= 0 || scene.Video.SourceInUS <= 0 || scene.Video.SourceDurationUS <= 0 || scene.Voiceover == nil || scene.Voiceover.AssetID == "" || scene.Voiceover.DurationUS <= 0 {
			t.Fatalf("scene lost canonical fields: %+v", scene)
		}
	}
	// timeline_start_us is the cumulative placement; end is always derived.
	var expectedStart int64
	for _, scene := range after {
		if scene.TimelineStartUS != expectedStart {
			t.Fatalf("scene %s timeline_start_us = %d, want contiguous %d", scene.ID, scene.TimelineStartUS, expectedStart)
		}
		expectedStart += scene.DurationUS
	}
}

func TestResolvedScenesCarryContiguousTimelineStart(t *testing.T) {
	scenes := []Scene{
		{ID: "scene-0", Index: 0, DurationUS: 3_000_000},
		{ID: "scene-1", Index: 1, DurationUS: 5_000_000},
		{ID: "scene-2", Index: 2, DurationUS: 2_000_000},
	}
	resolved, err := ResolveScenes(scenes, "it", audio.AudioModeNone, false)
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []int64{0, 3_000_000, 8_000_000}
	for i, want := range wantStarts {
		if resolved[i].TimelineStartUS != want {
			t.Fatalf("scene %d timeline_start_us = %d, want %d", i, resolved[i].TimelineStartUS, want)
		}
	}
	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSONField(t, raw, "timeline_start_us") {
		t.Fatalf("resolved scenes JSON must carry timeline_start_us: %s", raw)
	}
}

func TestSceneDurationResolverCombinedClipBoundFreezesUnderLongerVoiceover(t *testing.T) {
	// wiring seals the clip's visual span into DurationUS (16s); the certified
	// voiceover is longer (17.52s). COMBINED_TIMELINE must freeze the clip
	// under the narration, so the canonical duration is the voiceover span.
	clip := &ClipReference{ID: "clip-16", SourceInMS: 0, SourceOutMS: 16000, AudioPath: "/media/clip-16.m4a"}
	scenes := []Scene{{
		ID:         "scene-8",
		Index:      0,
		DurationUS: 16_000_000,
		Clip:       clip,
		Clips:      []*ClipReference{clip},
		Audio:      audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
		},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-8", FilePath: "/media/vo-8.m4a", Duration: 17.52}},
	}}
	resolved, err := ResolveScenes(scenes, "it", audio.AudioModeCombinedTimeline, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[0].DurationUS; got != 17_520_000 {
		t.Fatalf("scene duration = %dus, want 17520000us (max(video 16s, voiceover 17.52s))", got)
	}
}

func TestSceneDurationResolverCombinedClipBoundClipWinsWhenLonger(t *testing.T) {
	// clip 18.8s is longer than the 17.544s narration: no freeze, the clip
	// duration is the canonical scene duration.
	clip := &ClipReference{ID: "clip-18", SourceInMS: 0, SourceOutMS: 18800, AudioPath: "/media/clip-18.m4a"}
	scenes := []Scene{{
		ID:         "scene-0",
		Index:      0,
		DurationUS: 18_800_000,
		Clip:       clip,
		Clips:      []*ClipReference{clip},
		Audio:      audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-18", SourceInUS: 0, SourceDurationUS: 18_800_000, UseOriginalAudio: true},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-18", SourceInUS: 0, SourceDurationUS: 18_800_000, UseOriginalAudio: true},
		},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-0", FilePath: "/media/vo-0.m4a", Duration: 17.544}},
	}}
	resolved, err := ResolveScenes(scenes, "it", audio.AudioModeCombinedTimeline, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[0].DurationUS; got != 18_800_000 {
		t.Fatalf("scene duration = %dus, want 18800000us (max(video 18.8s, voiceover 17.544s))", got)
	}
}

func TestSceneDurationResolverNonCombinedKeepsEditorialDuration(t *testing.T) {
	// max() belongs to COMBINED_TIMELINE only. In any other mode the explicit
	// editorial duration still wins, even when the narration is longer.
	clip := &ClipReference{ID: "clip-16", SourceInMS: 0, SourceOutMS: 16000, AudioPath: "/media/clip-16.m4a"}
	scenes := []Scene{{
		ID:         "scene-8",
		Index:      0,
		DurationUS: 16_000_000,
		Clip:       clip,
		Clips:      []*ClipReference{clip},
		Audio:      audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-16", SourceInUS: 0, SourceDurationUS: 16_000_000, UseOriginalAudio: true},
		},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-8", FilePath: "/media/vo-8.m4a", Duration: 17.52}},
	}}
	resolved, err := ResolveScenes(scenes, "it", audio.AudioModeNone, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[0].DurationUS; got != 16_000_000 {
		t.Fatalf("scene duration = %dus, want 16000000us (editorial duration wins outside COMBINED_TIMELINE)", got)
	}
}

func TestSceneDurationResolverCombinedAudioOnlyIgnoresClipSpan(t *testing.T) {
	// The clip is evidence metadata, not materialized video. A long clip must
	// never stretch a VO-governed scene, so the sealed narration duration
	// (30s) wins over max(45s, 30s).
	clip := &ClipReference{ID: "clip-45", SourceInMS: 0, SourceOutMS: 45000, AudioPath: "/media/clip-45.m4a"}
	scenes := []Scene{{
		ID:         "scene-0",
		Index:      0,
		DurationUS: 30_000_000,
		Clip:       clip,
		Clips:      []*ClipReference{clip},
		Audio:      audio.AudioIntent{Mode: audio.AudioClip, ClipAssetID: "clip-45", SourceInUS: 0, SourceDurationUS: 45_000_000, UseOriginalAudio: true},
		AudioIntents: []audio.AudioIntent{
			{Mode: audio.AudioClip, ClipAssetID: "clip-45", SourceInUS: 0, SourceDurationUS: 45_000_000, UseOriginalAudio: true},
		},
		Voiceover: map[Language]AudioReference{"it": {ID: "vo-0", FilePath: "/media/vo-0.m4a", Duration: 30.0}},
	}}
	resolved, err := ResolveScenes(scenes, "it", audio.AudioModeCombinedTimeline, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[0].DurationUS; got != 30_000_000 {
		t.Fatalf("scene duration = %dus, want 30000000us (audio-only must not stretch to the clip span)", got)
	}
}

func containsJSONField(t *testing.T, raw []byte, field string) bool {
	t.Helper()
	var wire []map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, m := range wire {
		if _, ok := m[field]; !ok {
			return false
		}
	}
	return true
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
