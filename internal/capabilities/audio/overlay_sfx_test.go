package audio

import "testing"

func overlaySFXTestTimeline() CanonicalTimeline {
	return CanonicalTimeline{
		Version:    TimelineVersion,
		DurationUS: 10_000_000,
		Segments: []TimelineSegment{{
			ID: "scene_1", Index: 0, TimelineStartUS: 0, DurationUS: 10_000_000,
		}},
	}
}

func overlaySFXTestPlan(t *testing.T) *CompiledAudioPlan {
	t.Helper()
	plan := CompiledAudioPlan{
		Version:         AudioPlanVersion,
		TimelineVersion: TimelineVersion,
		DurationUS:      10_000_000,
		Output:          DefaultAudioProfile().Output(),
		Tracks: []AudioTrack{{
			TrackID: "voice", Role: TrackVoiceover,
			Events: []AudioEvent{{
				EventID: "scene_1-silence-0", Type: EventSilence,
				TimelineStartUS: 0, DurationUS: 10_000_000,
			}},
		}},
	}
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal fixture plan: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("fixture plan must validate: %v", err)
	}
	return &plan
}

func TestAppendSFXLayersAddsTrackAndReseals(t *testing.T) {
	plan := overlaySFXTestPlan(t)
	before := plan.PlanSHA256
	layers := []AudioLayer{{
		AssetID: "whoosh1", TimelineStartUS: 2_000_000, DurationUS: 250_000, GainDB: -18,
	}}
	if err := plan.AppendSFXLayers(overlaySFXTestTimeline(), layers); err != nil {
		t.Fatalf("append overlay sfx: %v", err)
	}
	var sfx *AudioTrack
	for index := range plan.Tracks {
		if plan.Tracks[index].Role == TrackSFX {
			sfx = &plan.Tracks[index]
		}
	}
	if sfx == nil || len(sfx.Events) != 1 {
		t.Fatalf("sfx track = %+v, want one event", sfx)
	}
	event := sfx.Events[0]
	if event.EventID != "overlay-sfx-0" || event.Type != EventSFX || event.AssetID != "whoosh1" ||
		event.TimelineStartUS != 2_000_000 || event.DurationUS != 250_000 ||
		event.SourceDurationUS != event.DurationUS || event.GainDB != -18 {
		t.Fatalf("sfx event = %+v", event)
	}
	if plan.PlanSHA256 == before {
		t.Fatal("appending an event must re-seal the plan digest")
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("extended plan must validate: %v", err)
	}
}

func TestAppendSFXLayersFailsClosed(t *testing.T) {
	plan := overlaySFXTestPlan(t)
	outside := []AudioLayer{{AssetID: "whoosh1", TimelineStartUS: 9_900_000, DurationUS: 250_000}}
	if err := plan.AppendSFXLayers(overlaySFXTestTimeline(), outside); err == nil {
		t.Fatal("a cue past the timeline end must fail closed")
	}
	duplicate := []AudioLayer{{AssetID: "whoosh1", TimelineStartUS: 0, DurationUS: 250_000}}
	if err := plan.AppendSFXLayers(overlaySFXTestTimeline(), duplicate); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := plan.AppendSFXLayers(overlaySFXTestTimeline(), duplicate); err == nil {
		t.Fatal("re-appending the same cue must fail closed, not duplicate the event")
	}
}
