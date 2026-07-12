package drive

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── FASE D: DoD 9 tests (July 2026) — YouTube→Publisher PathBuilder & category ──
//
// DoD 9 validates the YouTube clip publishing flow with semantic metadata:
//   - Category=Boxe paired with Subject=Pacquiao vs Broner (real-world names)
//   - PathBuilder segment sanitisation (SafeFolderName preserves spaces/hyphens)
//   - No-FolderID scenario (pure semantic routing — no RootFolderOverride)
//
// godlike/06 SSOT: YouTubeClipPath is the SOLE canonical owner of the
// clips/{group}/{subject} path structure. Category on PublishRequest is
// carried for Qdrant payload enrichment; the PathBuilder consumes Group
// and Subject only.

// TestYouTubeClipPath_CategoryBoxeSubjectPacquiaoVsBroner_DOD_9_1 pins
// DoD 9 item 1: YouTubeClipPath with the canonical Boxe category. The
// segments MUST be ["Boxe", "Pacquiao vs Broner"] after SafeFolderName
// sanitisation (which preserves alphanum, spaces, and hyphens).
func TestYouTubeClipPath_CategoryBoxeSubjectPacquiaoVsBroner_DOD_9_1(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err, "YouTubeClipPath must succeed with Group=Boxe and Subject='Pacquiao vs Broner'")

	// SafeFolderName preserves alphanum, spaces, and hyphens verbatim —
	// "Boxe" and "Pacquiao vs Broner" pass through unchanged.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, segs,
		"YouTubeClipPath must return canonical [{group},{subject}] segments — SafeFolderName preserves spaces and hyphens")
}

// TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2 pins DoD 9 item 2:
// YouTubeClipPath is idempotent across N retries with the same inputs.
// A future refactor that introduces non-deterministic segment order or
// timestamp-suffix injection would surface as a failing test.
func TestYouTubeClipPath_IsDeterministicAcrossRetries_DOD_9_2(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "Boxe",
		Subject: "Pacquiao vs Broner",
	}

	first, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		retry, err := delivery.YouTubeClipPath(req)
		require.NoError(t, err, "retry %d must not fail", i)
		require.Equal(t, first, retry,
			"retry %d: YouTubeClipPath must be byte-stable idempotent — same inputs must produce same segments", i)
	}
}

// TestYouTubeClipPath_PathBuilderSanitisesSpecialCharacters_DOD_9_3 pins
// DoD 9 item 3: PathBuilder sanitises special characters via SafeFolderName.
// Forward slashes, colons, and other OS-unsafe characters are replaced.
func TestYouTubeClipPath_PathBuilderSanitisesSpecialCharacters_DOD_9_3(t *testing.T) {
	// Subject with a YouTube-style video ID containing special chars
	// that SafeFolderName would replace (slashes → spaces/hyphens).
	req := delivery.PublishRequest{
		Group:   "NBA / Highlights",
		Subject: "game:7/OT",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	// SafeFolderName replaces / and : with safe alternatives (spaces/hyphens).
	require.Equal(t, 2, len(segs), "must produce exactly 2 segments [{group},{subject}]")
	// Positive assertions: SafeFolderName replaces non-alphanum (except -/_) with _.
	require.Equal(t, "NBA _ Highlights", segs[0],
		"SafeFolderName must replace / with _ in group segment")
	require.Equal(t, "game_7_OT", segs[1],
		"SafeFolderName must replace / with _ and : with _ in subject segment")
}

// TestYouTubeClipPath_WithRootFolderOverride_UsesSingleLeaf pins the
// override-aware clip path contract used by payload-selected roots:
// when RootFolderOverride is set, the path builder must emit a single
// leaf folder instead of the legacy youtube_uncategorized/group layer.
func TestYouTubeClipPath_WithRootFolderOverride_UsesSingleLeaf(t *testing.T) {
	req := delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Subject:            "qQIsvIOQS8U",
	}

	segs, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"qQIsvIOQS8U"}, segs)

	req = delivery.PublishRequest{
		RootFolderOverride: "explicit-root",
		Group:              "boxing-channels",
	}
	segs, err = delivery.YouTubeClipPath(req)
	require.NoError(t, err)
	require.Equal(t, []string{"boxing-channels"}, segs)
}

// ── FASE D: DoD 10 tests (July 2026) — fake FolderManager EnsureFolder integration ──
//
// DoD 10 validates the Publisher→FolderManager integration contract:
//   - fake FolderManager records each EnsureFolder(parent, segments...) call
//   - Assert that EnsureFolder is called with the CORRECT parent (registry root)
//     and the CORRECT segments (PathBuilder output after SafeFolderName)
//   - Verify the Category field is carried through PublishRequest to the result
//     (even though YouTubeClipPath doesn't consume it, the field survives the round-trip)
//
// godlike/06 SSOT: the fake FolderManager is the canonical test double for
// FolderManagerPort; assertors MUST probe folderCalls by index, verifying
// parent + segments shape, not just the leaf folder ID.

// TestPublisher_PublishYouTubeClip_CategoryBoxe_EnsureFolderSegments_DOD_10_1
// pins DoD 10 item 1: Publisher.Publish with Category=Boxe, Subject='Pacquiao vs
// Broner'. The fake FolderManager MUST record exactly one EnsureFolder call
// with parent="clips-root" and segments=["Boxe", "Pacquiao vs Broner"].
//
// This is the canonical integration-contract test: it proves the Publisher
// correctly routes the semantic metadata through the PathBuilder and into
// the FolderManager adapter without requiring a real Drive API.
//
// Note: Category is SET on the request but NOT asserted in the result —
// YouTubeClipPath consumes only Group+Subject for path resolution; Category
// is additive metadata carried downstream (Qdrant payload / outbox events)
// by the callers that consume PublishResult. The Publisher is transport-only
// and does not re-derive Category.
func TestPublisher_PublishYouTubeClip_CategoryBoxe_EnsureFolderSegments_DOD_10_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "boxe-pacquiao-broner-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/pacquiao_broner_clip.mp4",
		Filename:    "pacquiao_broner_clip.mp4",
		Description: "Pacquiao vs Broner highlights",
		AssetID:     "yt-pacquiao-broner-001",
		Group:       "Boxe",
		Subject:     "Pacquiao vs Broner",
		Category:    "Boxe",
		// No RootFolderOverride — pure semantic routing via registry.
	})
	require.NoError(t, err)

	// (1) PublishResult carries the resolved folder and path segments.
	require.Equal(t, "boxe-pacquiao-broner-folder-id", result.FolderID,
		"PublishResult.FolderID must equal the leaf folder returned by EnsureFolder")
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, result.PathSegments,
		"PublishResult.PathSegments must be the canonical [{group},{subject}] structure")
	require.Equal(t, "Boxe/Pacquiao vs Broner", result.FolderPath,
		"PublishResult.FolderPath must be the slash-joined display form of PathSegments")
	require.Equal(t, delivery.DestinationYouTubeClip, result.Destination,
		"PublishResult.Destination must echo back the requested DestinationKey")

	// (2) DoD 10 integration contract: the fake FolderManager recorded exactly
	//     one EnsureFolder call with the CORRECT parent and segments.
	require.Len(t, folders.ensureCalls, 1,
		"exactly one EnsureFolder call must be made (single Publish call)")
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent,
		"EnsureFolder parent must be the registry root folder ID 'clips-root'")
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, folders.ensureCalls[0].segments,
		"EnsureFolder segments must be the canonical [{group},{subject}] structure after PathBuilder + SafeFolderName")

	// (3) The uploader was called with the resolved folder.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "boxe-pacquiao-broner-folder-id", files.uploadCalls[0].folderID,
		"upload must land in the folder returned by EnsureFolder")
	require.Equal(t, "pacquiao_broner_clip.mp4", files.uploadCalls[0].filename,
		"upload filename must match the requested filename verbatim")
	require.Equal(t, "Pacquiao vs Broner highlights", files.uploadCalls[0].description,
		"upload description must match the requested description verbatim")
}

// TestPublisher_PublishYouTubeClip_NoFolderOverride_PureSemanticRouting_DOD_10_2
// pins DoD 10 item 2: the no-FolderID / no-RootFolderOverride scenario.
// The Publisher resolves everything through the registry (root + PathBuilder),
// and the caller never touches a folder ID. Proves the semantic-routing
// contract: Group + Subject are the ONLY required fields; Category is additive
// metadata carried through PublishResult without affecting path resolution.
func TestPublisher_PublishYouTubeClip_NoFolderOverride_PureSemanticRouting_DOD_10_2(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "semantic-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	// Pure semantic routing: no RootFolderOverride, no FolderID — just
	// Group + Subject + Category. The Publisher resolves everything.
	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/semantic_clip.mp4",
		Filename:    "semantic_clip.mp4",
		Group:       "Boxe",
		Subject:     "Pacquiao vs Broner",
		Category:    "Boxe",
		// RootFolderOverride intentionally omitted — zero value.
	})
	require.NoError(t, err)

	// (1) EnsureFolder was called with registry root, NOT an override.
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent,
		"when RootFolderOverride is empty, EnsureFolder parent MUST be the registry root 'clips-root'")

	// (2) Result carries the canonical path.
	require.Equal(t, []string{"Boxe", "Pacquiao vs Broner"}, result.PathSegments)
	require.Equal(t, "semantic-folder-id", result.FolderID)

	// (3) Upload landed in the semantic folder.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "semantic-folder-id", files.uploadCalls[0].folderID)
}

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
