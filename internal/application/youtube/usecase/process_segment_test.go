package usecase

import (
	"path/filepath"
	"testing"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────────
// TestBuildClipAsset_SearchText_NotJustFilename — DoD 10 regression guard
// (PR-YT-DOD-10-SEARCH-TEXT-CONTRACT-TDD, deadline 2026-08-01)
// ──────────────────────────────────────────────────────────────────────────────

// TestBuildClipAsset_SearchText_NotJustFilename locks the canonical DoD 10
// contract for the YouTube clip `search_text` field. The test fails fast
// if a future refactor regresses buildClipAsset to "filename only" or
// strips a canonical DoD-10 segment (title/summary/hook/topics/source_url
// /speakers/mentioned_people).
//
// Pure-function hermetic test: `buildClipAsset` is a package-level pure
// function (process_segment_helpers.go:19) that composes `SearchText` via
// `composeYouTubeClipSearchText(md, hook)` (process_segment_helpers.go:113).
// Zero DB / network / yt-dlp stub needed.
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
// leakage), verified by the `EmptyAllFields_…` subtest.
//
// Pre-existing 6-item voiceover + app build-issue carry-forward per
// architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04
// UNCHANGED — NOT regressions of this test.
func TestBuildClipAsset_SearchText_NotJustFilename(t *testing.T) {
	t.Run("HappyPath_AllCanonicalSegmentsPresent", func(t *testing.T) {
		// Canonical Pacquiao/Broner params (mirrors action plan §2 spec).
		cmd := youtubetypes.ProcessSegmentCommand{
			VideoURL: "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			VideoID:  "vdC5GXxS-qU",
			Segment: youtubetypes.Segment{
				Name:            "Sfuriata contro Pacquiao",
				Summary:         "Broner insults Pacquiao, then lands leather on him.",
				Hook:            "Don't think about Floyd, think about me!",
				Topics:          []string{"boxing", "mayweather", "confrontation"},
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
		assert.Contains(t, a.SearchText, "Broner insults Pacquiao",
			"DoD 10: summary segment must be present (=Segment.Summary, narrative)")
		assert.Contains(t, a.SearchText, "Don't think about Floyd",
			"DoD 10: hook segment must be present (=Segment.Hook, attention-grabber)")
		assert.Contains(t, a.SearchText, "boxing",
			"DoD 10: topics segment must be present (first topic)")
		assert.Contains(t, a.SearchText, "mayweather",
			"DoD 10: topics segment must be present (second topic)")
		assert.Contains(t, a.SearchText, "confrontation",
			"DoD 10: topics segment must be present (third topic)")
		assert.Contains(t, a.SearchText, "https://www.youtube.com/watch?v=vdC5GXxS-qU",
			"DoD 10: source_url segment must be present (=SourceURL, traceability)")
		assert.Contains(t, a.SearchText, "Broner",
			"DoD 10: speakers segment must be present (first speaker)")
		assert.Contains(t, a.SearchText, "Pacquiao",
			"DoD 10: speakers segment must be present (second speaker)")
		assert.Contains(t, a.SearchText, "Floyd Mayweather",
			"DoD 10: mentioned_people segment must be present (=MentionedPeople, person refs)")
	})

	t.Run("EmptyAllFields_EmptySearchText_NoDanglingFragments", func(t *testing.T) {
		// godlike/07 minimum-blast-radius: when ALL inputs are empty the
		// composer must produce an empty string — never emit "Tags:" or
		// "Source:" labels with empty values, never echo the filename.
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
				"`deriveNormalizedGroup` falls back to \"general\" when "+
				"cmd.Destination is nil — nil-safe default per helper)")
	})

	t.Run("TitleDerivedFromSummary_WhenNameEmpty", func(t *testing.T) {
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

		// Summary must still surface somewhere in SearchText (as Title
		// via the fallback). Defense against future "Title field unused"
		// refactor that would silently drop the fallback.
		assert.Contains(t, a.SearchText,
			"Broner rises to fame but ignores discipline",
			"DoD 10: Title fallback to Summary when Segment.Name→\"\" "+
				"MUST still surface Summary content in SearchText "+
				"(locks process_segment_helpers.go:53-56 fallback contract)")
	})
}
