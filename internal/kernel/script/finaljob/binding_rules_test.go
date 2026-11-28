package finaljob

import (
	"strconv"
	"strings"
	"testing"
)

func TestValidatePlanBindingRulesReportsEveryFrameDurationMismatch(t *testing.T) {
	sha := strings.Repeat("a", 64)
	plan := CompiledPlanV2{
		Output: PlanOutput{FPSNum: 24, FPSDen: 1},
		VideoTracks: []PlanVideoTrack{{TrackID: "main", Segments: []PlanSegment{
			{SegmentID: "bad-1", AssetID: "video", SHA256: sha, FrameCount: 220, SourceDurationUS: 10_250_000},
			{SegmentID: "bad-2", AssetID: "video", SHA256: sha, TimelineStartFrame: 220, FrameCount: 24, SourceInUS: 10_250_000, SourceDurationUS: 1_100_000},
		}}},
		Assets: []PlanAsset{{AssetID: "video", SHA256: sha, SizeBytes: 100, Kind: "video", DurationUS: 20_000_000}},
	}
	err := ValidatePlanBindingRules(plan)
	if err == nil {
		t.Fatal("expected frame-duration mismatch")
	}
	if got := strings.Count(err.Error(), "does not match frame_count"); got != 2 {
		t.Fatalf("reported %d duration violations, want both; error: %v", got, err)
	}
}

func TestValidatePlanBindingRulesReproducesGratteriR15R22R28(t *testing.T) {
	sha := strings.Repeat("b", 64)
	cases := []struct {
		name             string
		frames           int64
		sourceDuration   int64
		expectedDuration int64
	}{
		{name: "r15", frames: 179, sourceDuration: 5_000_000, expectedDuration: 7_458_333},
		{name: "r22", frames: 1_233, sourceDuration: 54_164_000, expectedDuration: 51_375_000},
		{name: "r28", frames: 220, sourceDuration: 10_250_000, expectedDuration: 9_166_666},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := CompiledPlanV2{
				Output: PlanOutput{FPSNum: 24, FPSDen: 1},
				VideoTracks: []PlanVideoTrack{{TrackID: "main", Segments: []PlanSegment{{
					SegmentID: tc.name, AssetID: "video", SHA256: sha,
					FrameCount: tc.frames, SourceDurationUS: tc.sourceDuration,
				}}}},
				Assets: []PlanAsset{{AssetID: "video", SHA256: sha, SizeBytes: 100, Kind: "video", DurationUS: 60_000_000}},
			}
			if err := ValidatePlanBindingRules(plan); err == nil || !strings.Contains(err.Error(), "expected "+strconv.FormatInt(tc.expectedDuration, 10)) {
				t.Fatalf("ValidatePlanBindingRules error = %v, want frame-exact expected duration %d", err, tc.expectedDuration)
			}
			plan.VideoTracks[0].Segments[0].SourceDurationUS = tc.expectedDuration
			if err := ValidatePlanBindingRules(plan); err != nil {
				t.Fatalf("frame-exact segment rejected: %v", err)
			}
		})
	}
}

func TestValidatePlanBindingRulesCollectsIndependentViolations(t *testing.T) {
	sha := strings.Repeat("c", 64)
	plan := CompiledPlanV2{
		Output: PlanOutput{FPSNum: 24, FPSDen: 1},
		VideoTracks: []PlanVideoTrack{{TrackID: "main", Segments: []PlanSegment{
			{SegmentID: "mismatch", AssetID: "video", SHA256: sha, FrameCount: 24, SourceDurationUS: 900_000},
			{SegmentID: "out-of-bounds", AssetID: "video", SHA256: sha, FrameCount: 24, SourceInUS: 900_001, SourceDurationUS: 1_000_000},
			{SegmentID: "mismatch", AssetID: "video", SHA256: sha, FrameCount: 24, SourceDurationUS: 1_000_000},
		}}},
		Assets: []PlanAsset{{AssetID: "video", SHA256: sha, SizeBytes: 100, Kind: "video", DurationUS: 1_000_000}},
	}
	err := ValidatePlanBindingRules(plan)
	if err == nil {
		t.Fatal("expected several independent binding violations")
	}
	for _, want := range []string{"does not match frame_count", "source_range_out_of_bounds", "repeat id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("combined error lacks %q: %v", want, err)
		}
	}
}
