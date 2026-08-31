package scriptgeneration

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildGenerateRequest_ForceRefreshReachesAllGenerationCaches(t *testing.T) {
	env := &scriptpkg.GenerationEnvelopeV2{
		Version:      2,
		ForceRefresh: true,
		Items: []scriptpkg.GenerationItemV2{{
			ID:       "vidrush-cold",
			Title:    "VidRush cold certification",
			Language: "en",
			Source: scriptpkg.SourceSpec{
				Type:  scriptpkg.SourceText,
				Topic: "independent scenes",
			},
			ScriptParams: scriptpkg.ScriptSpec{
				Segments: []scriptpkg.ScriptSegment{
					{ID: "coastal-road", Topic: "coastal road", SourceText: "A drone follows a coastal highway.", TargetWords: 40},
					{ID: "latte-art", Topic: "latte art", SourceText: "A barista pours latte art.", TargetWords: 40},
					{ID: "trail-runner", Topic: "trail runner", SourceText: "A trail runner climbs a mountain ridge.", TargetWords: 40},
				},
			},
			MediaPlan: mediadomain.MediaPlanSpec{},
		}},
	}

	got, err := BuildGenerateRequest(env, "vidrush-cold-key")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ForceRefresh {
		t.Fatal("envelope force_refresh must reach GenerateRequest")
	}
	if !got.Source.ForceRefresh {
		t.Fatal("envelope force_refresh must bypass source cache")
	}
	if !got.ScriptParams.ForceRefresh {
		t.Fatal("envelope force_refresh must bypass script memory cache")
	}
	if !got.MediaPlan.ForceRefreshExtraction {
		t.Fatal("envelope force_refresh must bypass VidRush extraction cache")
	}
	if !got.MediaPlan.ForceRefreshAssets {
		t.Fatal("envelope force_refresh must bypass VidRush provider/materialization cache")
	}
	if len(got.ScriptParams.Segments) != 3 {
		t.Fatalf("explicit scene topology lost while applying force_refresh: got %d segments", len(got.ScriptParams.Segments))
	}
	for i, want := range []string{"coastal-road", "latte-art", "trail-runner"} {
		if got.ScriptParams.Segments[i].ID != want {
			t.Fatalf("segment[%d] id=%q want=%q", i, got.ScriptParams.Segments[i].ID, want)
		}
	}
}
