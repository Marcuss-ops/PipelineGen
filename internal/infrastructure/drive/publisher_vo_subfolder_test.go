// Package drive — publisher_vo_subfolder_test.go: PR-VO-3-LANGUAGE-MATRIX
// Drive subfolder matrix test. Step 6 split (July 2026): extracted from
// the previous `publisher_test_dod_language_test.go` (which held 7
// tests across 3 FASE categories). The Voiceover 3-language matrix test
// lives here in its own file because it's the canonical voiceover
// multi-language Drive subpath regression guard.
//
// godlike/06 SSOT: VoiceoverPath is the SOLE canonical owner of the
// voiceovers/{project}/{language} path structure. SafeFolderName is
// the canonical segment-sanitiser (preserves alphanum + hyphen verbatim).
//
// 1 test under this header covers:
//
//	3 Publish calls with (it-IT, pt-BR, en-US) against the same project
//	MUST produce 3 DISTINCT EnsureFolder calls with distinct
//	{project}/{language} segments.
//
// These tests FAIL on regression only if:
//   - PathBuilder is short-circuited when RootFolderOverride is set
//     (pre-PR-VO-SUBFOLDER silent overwrite bug — all 3 languages
//     would have landed in the SAME override root folder, silently
//     overwriting each other's MP3 files)
package drive

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── PR-VO-3-LANGUAGE-MATRIX tests (July 2026) ─────────────────────────
//
// Pins the canonical voiceover multi-language Drive subpath contract:
// 3 Publish calls with (it-IT, pt-BR, en-US) against the same project
// MUST produce 3 DISTINCT EnsureFolder calls with distinct
// {project}/{language} segments, proving each language gets its own
// Drive subfolder.
//
// Pre-PR-VO-SUBFOLDER, PathBuilder was short-circuited when
// RootFolderOverride was set, so ALL languages landed in the SAME
// override root folder — silently overwriting each other's MP3 files.
// After the fix, each language resolves to a distinct
// {project}/{language} subfolder under the override.
//
// godlike/06 SSOT: VoiceoverPath is the SOLE canonical owner of the
// voiceovers/{project}/{language} path structure.
//
// References:
//   - VoiceoverPath: internal/application/assets/delivery/registry.go
//     (segments = [project, language] when both non-empty).
//   - SafeFolderName: pkg/pathutil/pathutil.go (preserves alphanum +
//     hyphen verbatim, so "it-IT" / "pt-BR" / "en-US" pass through).
//   - PR-VO-SUBFOLDER: fix in publisher.go::resolveDestination.
//   - PR-VOICEOVER-PROJECT-THREADING: Project field threading fix.

// TestPublisher_Voiceover_3LanguageMatrix_DistinctSubpaths pins the
// multi-language Drive subpath contract: 3 Publish calls with different
// languages against the same project MUST produce 3 DISTINCT
// EnsureFolder calls with distinct {project}/{language} segments.
//
// This is the canonical regression guard for the silent-overwrite bug:
// pre-PR-VO-SUBFOLDER, all 3 languages would have landed in the SAME
// override root folder (single EnsureFolder call with no segments),
// silently overwriting each other's MP3 files.
func TestPublisher_Voiceover_3LanguageMatrix_DistinctSubpaths(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "vo-sub-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	project := "yt-test-voiceover-drive"
	override := "explicit-voiceover-folder-id"

	type langCase struct {
		lang     string
		filename string
	}
	languages := []langCase{
		{"it-IT", "storia-boxe-it.mp3"},
		{"pt-BR", "storia-boxe-pt.mp3"},
		{"en-US", "storia-boxe-en.mp3"},
	}

	for _, lc := range languages {
		_, err := pub.Publish(context.Background(), delivery.PublishRequest{
			Destination:        delivery.DestinationVoiceover,
			LocalPath:          "/tmp/" + lc.filename,
			Filename:           lc.filename,
			ProjectID:          project,
			Language:           lc.lang,
			RootFolderOverride: override,
		})
		require.NoError(t, err, "Publish for language %q must succeed", lc.lang)
	}

	// (1) Exactly 3 EnsureFolder calls — one per language.
	require.Len(t, folders.ensureCalls, 3,
		"3 languages MUST produce 3 distinct EnsureFolder calls — not 1 (pre-fix bug) and not 0")

	// (2) Each EnsureFolder call uses the SAME parent (override) but
	//     DISTINCT segments [{project}, {language}].
	expectedSegments := map[string][]string{
		"it-IT": {project, "it-IT"},
		"pt-BR": {project, "pt-BR"},
		"en-US": {project, "en-US"},
	}
	seen := make(map[string]bool)
	for _, call := range folders.ensureCalls {
		require.Equal(t, override, call.parent,
			"EnsureFolder parent MUST be the explicit override for all languages")
		require.Len(t, call.segments, 2,
			"Each EnsureFolder MUST have exactly 2 segments [{project}, {language}]")
		require.Equal(t, project, call.segments[0],
			"First segment MUST be the project name")
		lang := call.segments[1]
		expected, ok := expectedSegments[lang]
		require.True(t, ok, "Unexpected language segment %q in EnsureFolder call", lang)
		require.Equal(t, expected, call.segments,
			"EnsureFolder segments for %q MUST be [{project}, {lang}]", lang)
		seen[lang] = true
	}
	require.Len(t, seen, 3,
		"All 3 languages MUST appear exactly once in the EnsureFolder calls")
	require.True(t, seen["it-IT"] && seen["pt-BR"] && seen["en-US"],
		"All 3 languages (it-IT, pt-BR, en-US) must be present in EnsureFolder calls")

	// (3) Exactly 3 uploads — one per language.
	require.Len(t, files.uploadCalls, 3,
		"3 languages MUST produce 3 uploads")

	// (4) Each upload lands in the SAME sub-folder ID (fake returns
	//     the same result for all calls — in production, each language
	//     would have a different folder ID from Drive).
	for i, call := range files.uploadCalls {
		require.Equal(t, "vo-sub-folder-id", call.folderID,
			"Upload %d (%s) must land in the resolved sub-folder", i, call.filename)
	}
}
