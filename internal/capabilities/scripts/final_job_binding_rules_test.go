package scriptgeneration

import (
	"strings"
	"testing"
)

// TestValidateFinalJobPlanBindingRulesRejectsOutOfBoundsWindow pins the local
// half of the r36/r37 fix: the exact three coordinates the Master rejected must
// die here, with the same segment indexes, before PREPARE or any fragment upload.
func TestValidateFinalJobPlanBindingRulesRejectsOutOfBoundsWindow(t *testing.T) {
	sha := strings.Repeat("a", 64)
	plan := finalJobCompiledPlan{
		VideoTracks: []finalJobPlanVideoTrack{{
			TrackID: "video",
			Segments: []finalJobPlanSegment{{
				SegmentID:          "segment-0094",
				AssetID:            "asset-1",
				SHA256:             sha,
				TimelineStartFrame: 0,
				FrameCount:         24,
				SourceInUS:         14_000_000,
				SourceDurationUS:   1_013_000,
			}},
		}},
		Assets: []finalJobPlanAsset{{
			AssetID:    "asset-1",
			AssetKey:   "asset-1",
			SHA256:     sha,
			SizeBytes:  1024,
			Kind:       "video",
			DurationUS: 15_000_000,
		}},
	}
	err := validateFinalJobPlanBindingRules(plan)
	if err == nil {
		t.Fatal("an out-of-bounds source window must be rejected locally")
	}
	for _, token := range []string{
		"invalid_compiled_render_plan",
		"segments[0]",
		"source_range_out_of_bounds",
		"expected source_in_us + source_duration_us <= 15000000",
		"observed 14000000 + 1013000",
	} {
		if !strings.Contains(err.Error(), token) {
			t.Fatalf("error %q is missing %q", err, token)
		}
	}
}

// TestValidateFinalJobPlanBindingRulesAcceptsExactSourceEnd keeps the boundary
// inclusive: a window that ends exactly at the certified asset duration is valid.
func TestValidateFinalJobPlanBindingRulesAcceptsExactSourceEnd(t *testing.T) {
	sha := strings.Repeat("b", 64)
	plan := finalJobCompiledPlan{
		VideoTracks: []finalJobPlanVideoTrack{{
			TrackID: "video",
			Segments: []finalJobPlanSegment{{
				SegmentID:          "segment-0000",
				AssetID:            "asset-1",
				SHA256:             sha,
				TimelineStartFrame: 0,
				FrameCount:         24,
				SourceInUS:         14_000_000,
				SourceDurationUS:   1_000_000,
			}},
		}},
		Assets: []finalJobPlanAsset{{
			AssetID:    "asset-1",
			AssetKey:   "asset-1",
			SHA256:     sha,
			SizeBytes:  1024,
			Kind:       "video",
			DurationUS: 15_000_000,
		}},
	}
	if err := validateFinalJobPlanBindingRules(plan); err != nil {
		t.Fatalf("an exactly-ending source window must be accepted: %v", err)
	}
}
