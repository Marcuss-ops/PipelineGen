package usecase

import (
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// TestBuildClipAsset_SearchText_EmptyAllFields — DoD 10 minimum-blast-radius
// guard (PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD, deadline 2026-08-01)
//
// Step 1 split (July 2026): extracted from process_segment_test.go
// (canonical) as a top-level test for SSOT isolation. The edge case here
// is "all inputs empty" — a future refactor that introduces a dangling
// "Tags:" / "Source:" / filename-bleed fragment in the empty case
// surfaces here as a test failure.
//
// godlike/07 minimum-blast-radius: when ALL inputs are empty the
// composer must produce an empty string — never emit "Tags:" or
// "Source:" labels with empty values, never echo the filename.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion below is falsifiable.
// Failures surface as build/CI test failures (NOT silent data drift).
//
// godlike/06 SSOT: the EmptyAllFields invariant is the regression-guard
// for the empty-state branch of composeYouTubeClipSearchText. A
// regression that breaks this surfaces as test failure here, NOT as
// silent data drift in production.
// ──────────────────────────────────────────────────────────────────────────────

func TestBuildClipAsset_SearchText_EmptyAllFields(t *testing.T) {
	a := buildClipAsset(
		"yt_empty_clip",
		youtubetypes.ProcessSegmentCommand{
			VideoURL: "",
			VideoID:  "",
			Segment:  youtubetypes.Segment{}, // Name/Summary/Hook/Topics/Speakers/MentionedPeople all empty
		},
		youtubetypes.ProcessSegmentResult{
			Item: youtubetypes.ExtractItem{
				LocalPath: "/tmp/empty.mp4",
			},
		},
		"", "",
	)

	require.Equal(t, "", a.SearchText,
		"all-empty inputs MUST produce empty SearchText "+
			"(godlike/07 minimum-blast-radius: no dangling fragments; "+
			"`deriveNormalizedGroup` returns \"\" when "+
			"cmd.Destination is nil — delegates fallback to delivery.YouTubeClipPath (SSOT))")
}
