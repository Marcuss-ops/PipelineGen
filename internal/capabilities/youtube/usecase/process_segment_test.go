package usecase

import (
	"path/filepath"
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// TestBuildClipAsset_SearchText_NotJustFilename — DoD 10 regression guard
// (PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD, deadline 2026-08-01)
//
// Canonical (Step 1 split, July 2026): this file holds the primary DoD 10
// contract — the HappyPath_AllCanonicalSegmentsPresent scenario. Edge cases
// (EmptyAllFields, TitleDerivedFromSummary) are extracted to per-scenario
// test files for SSOT isolation:
//   - process_segment_empty_test.go        — empty inputs / dangling fragments
//   - process_segment_title_fallback_test.go — title fallback when Name empty
//
// godlike/06 SSOT (one canonical owner per fact):
//
//	composeYouTubeClipSearchText is the SOLE canonical owner of the
//	YouTube-clip search_text format at Step 9 write time. This test
//	is the canonical regression-guard for that contract — any future
//	agent touching the function chain buildClipAsset →
//	composeYouTubeClipSearchText must keep all subtests green.
//
// godlike/07 NO-FAKE-AVAILABILITY: every assertion below is falsifiable.
// Failures surface as build/CI test failures (NOT silent data drift).
//
// godlike/07 typed-error contract: matches the order documented in
// `composeYouTubeClipSearchText` (title → summary → hook → topics →
// source_url → speakers → mentioned_people). Empty values per segment
// are dropped silently (no dangling "Tags:" or "Source:" fragment
// leakage), verified by the `EmptyAllFields_…` subtest in
// process_segment_empty_test.go.
//
// Pre-existing 6-item voiceover + app build-issue carry-forward per
// architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04
// UNCHANGED — NOT regressions of this test.
// ──────────────────────────────────────────────────────────────────────────────

func TestBuildClipAsset_SearchText_NotJustFilename(t *testing.T) {
	t.Run("HappyPath_AllCanonicalSegmentsPresent", func(t *testing.T) {
		// Canonical Pacquiao/Broner params (mirrors action plan §2 spec).
		cmd := youtubetypes.ProcessSegmentCommand{
			VideoURL: "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			VideoID:  "vdC5GXxS-qU",
			Segment: youtubetypes.Segment{
				Name: "Sfuriata contro Pacquiao",
				// Summary intentionally DOES NOT contain "Broner" or "Pacquiao"
				// so that the speakers-segment assertions below are
				// uniquely attributable to the joined-Speakers branch of
				// composeYouTubeClipSearchText (not bleed-through from
				// Summary). Code-reviewer round-2 MUST-FIX-2026-07-08.
				Summary: "An insult on camera, then leather on him.",
				// Hook intentionally DOES NOT contain "Floyd" (it does
				// contain "thinks") so that the MentionedPeople
				// assertion for "Floyd Mayweather" is uniquely attributable
				// to the MentionedPeople branch.
				Hook:            "Stay focused! It is personal.",
				Topics:          []string{"boxing", "confrontation", "prefight"},
				Speakers:        []string{"Broner", "Pacquiao"},
				MentionedPeople: []string{"Floyd Mayweather"},
			},
			Destination: &youtubetypes.DestinationRequest{
				Group:    "boxing",
				FolderID: "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA",
			},
			DriveFolderID:   "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA",
			DriveFolderPath: "/Boxing/Pacquiao-Broner",
		}
		out := youtubetypes.ProcessSegmentResult{
			Item: youtubetypes.ExtractItem{
				LocalPath:    "/tmp/yt_vdC5GXxS-qU_146_155_v1_la-sfuriata-contro-pacquiao.mp4",
				DriveFileID:  "drive-file-id-001",
				DriveLink:    "https://drive.google.com/file/d/drive-file-id-001/view",
				StartSeconds: 146,
				EndSeconds:   155,
				Duration:     9,
			},
		}
		a := buildClipAsset(
			"yt_vdC5GXxS-qU_146_155_v1",
			cmd, out,
			"sha256-do-d10-contract-001",
			"v1",
		)

		// ── Pre-conditions ─────────────────────────────────────────────
		require.NotEmpty(t, a.SearchText,
			"DoD 10: search_text MUST be non-empty after buildClipAsset")

		// ── Regression guard #1: NOT just the filename ─────────────────
		// A future agent that swaps SearchText for `out.Item.LocalPath`
		// (or just the filename) would break semantic search AND Qdrant
		// BM25 embedding. Block that at test-fixture level.
		assert.NotEqual(t, filepath.Base(out.Item.LocalPath), a.SearchText,
			"DoD 10: search_text MUST NOT equal filename "+
				"(regression would surface as `yt_vdC5…_146_155_v1_la-sfuriata-contro-pacquiao.mp4` only)")
		assert.NotEqual(t, out.Item.LocalPath, a.SearchText,
			"DoD 10: search_text MUST NOT equal full LocalPath "+
				"(regression would leak raw filesystem path; "+
				"compares the full path string — URL slashes are not asserted "+
				"because source_url legitimately contains '/')")
		assert.NotContains(t, a.SearchText, "/tmp/",
			"DoD 10: search_text MUST NOT contain the '/tmp/' local-path prefix "+
				"(regression guard for LocalPath leakage; URL slashes are not "+
				"asserted because source_url legitimately contains '/')")
		assert.NotContains(t, a.SearchText, ".mp4",
			"DoD 10: search_text MUST NOT contain '.mp4' extension "+
				"(regression guard for filename leak via extension suffix — "+
				"the URL `https://...youtube.com/watch?v=vdC5GXxS-qU` does "+
				"not contain '.mp4' so this assertion is safe)")

		// ── Canonical DoD 10 segments present ──────────────────────────
		// Order in composeYouTubeClipSearchText (process_segment_helpers.go:113):
		// title → summary → hook → topics → source_url → speakers → mentioned_people
		assert.Contains(t, a.SearchText, "Sfuriata contro Pacquiao",
			"DoD 10: title segment must be present (=Segment.Name, primary identifier)")
		assert.Contains(t, a.SearchText, "An insult on camera",
			"DoD 10: summary segment must be present (=Segment.Summary, narrative)")
		assert.Contains(t, a.SearchText, "Stay focused",
			"DoD 10: hook segment must be present (=Segment.Hook, attention-grabber)")
		assert.Contains(t, a.SearchText, "boxing",
			"DoD 10: topics segment must be present (first topic)")
		assert.Contains(t, a.SearchText, "confrontation",
			"DoD 10: topics segment must be present (second topic)")
		assert.Contains(t, a.SearchText, "prefight",
			"DoD 10: topics segment must be present (third topic)")
		assert.Contains(t, a.SearchText, "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			"DoD 10: source_url segment must be present (=SourceURL, traceability)")
		// Lock speakers as joined "Broner Pacquiao" (NOT separate strings).
		// Post-CR-fixup: Summary no longer contains "Broner"/"Pacquiao", so
		// this joined-string assertion is UNIQUELY attributable to the
		// Speakers composer branch (no bleed-through from Summary).
		assert.Contains(t, a.SearchText, "Broner Pacquiao",
			"DoD 10: speakers segment MUST be present as joined string — "+
				"a regression that drops the Speakers branch surfaces here "+
				"(no bleed-through from Summary after CR round-2 fixup)")
		assert.Contains(t, a.SearchText, "Floyd Mayweather",
			"DoD 10: mentioned_people segment must be present (=MentionedPeople, person refs; "+
				"uniquely attributable — Hook no longer overlaps post-CR fixup)")
	})
}
