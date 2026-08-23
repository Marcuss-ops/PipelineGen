package usecase

import (
	"strings"
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// TestBuildClipAsset_SearchText_TitleDerivedFromSummary — DoD 10 typed-branch
// regression guard (PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD, deadline 2026-08-01)
//
// Step 1 split (July 2026): extracted from process_segment_test.go
// (canonical) as a top-level test for SSOT isolation. The edge case here
// is "Segment.Name empty → md.Title falls back to Segment.Summary" — a
// future refactor that drops the fallback surfaces here as a test
// failure (strings.Count != 2).
//
// godlike/07 typed-branch contract: when Segment.Name is empty,
// `buildClipAsset` falls back to Segment.Summary for md.Title
// (process_segment_helpers.go:53-56). Locking the fallback so a
// future refactor that drops the fallback surfaces as test fail.
//
// The fallback path puts Summary into md.Title, AND the composer
// ALSO appends md.Summary. So the literal substring must appear
// EXACTLY TWICE in SearchText — once via Title-via-fallback,
// once via Summary-via-direct. A regression that breaks either
// path surfaces as strings.Count != 2.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion below is falsifiable.
// Failures surface as build/CI test failures (NOT silent data drift).
// ──────────────────────────────────────────────────────────────────────────────

func TestBuildClipAsset_SearchText_TitleDerivedFromSummary(t *testing.T) {
	// godlike/07 typed-branch contract: when Segment.Name is empty,
	// `buildClipAsset` falls back to Segment.Summary for md.Title
	// (process_segment_helpers.go:53-56). Locking the fallback so a
	// future refactor that drops the fallback surfaces as test fail.
	cmd := youtubetypes.ProcessSegmentCommand{
		VideoURL: "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		VideoID:  "vdC5GXxS-qU",
		Segment: youtubetypes.Segment{
			Name:    "", // empty ⇒ md.Title falls back to Summary
			Summary: "Broner rises to fame but ignores discipline.",
		},
	}
	out := youtubetypes.ProcessSegmentResult{
		Item: youtubetypes.ExtractItem{
			LocalPath:    "/tmp/yt_test.mp4",
			DriveFileID:  "drive-x",
			DriveLink:    "https://drive.google.com/file/d/x/view",
			StartSeconds: 0,
			EndSeconds:   5,
			Duration:     5,
		},
	}
	a := buildClipAsset("yt_test", cmd, out, "sha256-x", "v1")

	// Title-fallback robust invariant (CR round-2 SHOULD-FIX-2026-07-08):
	// The fallback path puts Summary into md.Title, AND the composer
	// ALSO appends md.Summary. So the literal substring must appear
	// EXACTLY TWICE in SearchText — once via Title-via-fallback,
	// once via Summary-via-direct. A regression that breaks either
	// path surfaces as strings.Count != 2.
	const fallbackLiteral = "Broner rises to fame but ignores discipline."
	require.Equal(t, 2, strings.Count(a.SearchText, fallbackLiteral),
		"DoD 10: Title fallback to Summary must surface the Summary "+
			"content EXACTLY TWICE (once via md.Title-via-fallback, "+
			"once via md.Summary-via-direct) — "+
			"locks process_segment_helpers.go:53-56 fallback contract")
}
