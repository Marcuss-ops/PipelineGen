package drive

// idempotency surface tests for the drive publisher.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip.mp4",
		ParentFolderID: override,
		ConflictPolicy: delivery.ConflictOverwrite,
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
	})

	// (1) Dual-return shape assertions.
	require.NotNil(t, resolved,
		"resolveDestination must return a non-nil ResolvedDriveDestination even on typed-error path (dual-return shape contract)")
	require.Equal(t, override, resolved.RootFolderID,
		"resolved.RootFolderID must be the explicit override (root-folder fallback)")
	require.Empty(t, resolved.PathSegments,
		"resolved.PathSegments must be empty on the typed-error path (direct-to-root fallback)")
	require.Equal(t, override, resolved.FolderID,
		"resolved.FolderID must equal the explicit override (no nested folder created on typed-error path)")

	// (2) Typed-error contract: errors.Is the canonical sentinel.
	require.Error(t, err, "resolveDestination must surface a non-nil error on the typed-error path")
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForParent,
		"the returned error MUST wrap ErrPathBuilderIncompleteForParent (errors.Is gateway for call-site decision)")

	// (3) Typed-chain preservation + grep-able diagnostic surface.
	//     The dual-%w fmt.Errorf wrap is verified by:
	//       (3a) err.Error() contains "group is required" (the underlying
	//            cause's message survives the wrap via the %w chain)
	//       (3b) errors.Is(err, ErrPathBuilderIncompleteForParent)
	//            (the sentinel is recoverable via the wrap chain).
	//     We intentionally do NOT use errors.As(&recoveredCause) here
	//     because (a) the underlying cause is a private fmt.Errorf
	//     returned by policy.PathBuilder and is not typeable from
	//     outside, (b) `go vet` rejects `errors.As(err, &error)` as the
	//     target must be a concrete type, (c) the (3a) + (3b) checks
	//     together verify the chain-preservation contract without
	//     walk-order flakiness.
	require.Contains(t, err.Error(), "subject (video ID) is required",
		"dual-%w fmt.Errorf must preserve the underlying PathBuilder cause 'subject (video ID) is required' (typed-chain diagnostic via message-preservation — Group now falls back to youtube_uncategorized, Subject is first to fail)")
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForParent,
		"dual-%w fmt.Errorf must preserve ErrPathBuilderIncompleteForParent for errors.Is at the resolveDestination call site (call-site decision gateway)")
}

func TestResolveDestination_SuccessPath_ReturnsNilErr(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "leaf-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip.mp4",
		Group:          "test",
		Subject:        "abc",
		ParentFolderID: "explicit-override-folder-id",
		ConflictPolicy: delivery.ConflictOverwrite,
	})

	// Success path: PathBuilder succeeded → segments built → EnsureFolder
	// called → leaf folder returned. err MUST be nil on the success path
	// (the dual-return shape demands err=nil when nothing failed).
	require.NoError(t, err, "resolveDestination MUST return nil err on success path (paired with fallback-path test for non-nil err)")

	// Resolved struct carries the leaf folder id (returned by fakeFolderManager).
	require.NotNil(t, resolved)
	require.Equal(t, "leaf-folder-id", resolved.FolderID,
		"success path: FolderID must equal the leaf folder returned by EnsureFolder")
	require.NotEmpty(t, resolved.PathSegments,
		"success path: PathSegments must be non-empty when PathBuilder succeeds")
	require.Equal(t, []string{"test", "abc"}, resolved.PathSegments,
		"success path: PathSegments must remain the canonical [{group},{subject}] shape when ParentFolderID only changes the root folder")
	require.Equal(t, "explicit-override-folder-id", resolved.RootFolderID,
		"success path: RootFolderID must be the explicit override (ParentFolderID precedence)")
}

func TestResolveDestination_PathBuilderFailOverride_UsesOverrideRoot(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip.mp4",
		ParentFolderID: override,
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err,
		"Publish MUST swallow ErrPathBuilderIncompleteForParent at the call-site (backward-compat per godlike/07 minimum-blast-radius)")

	// Upload landed in the override root via the resolved struct (proves dual-return shape contract).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Publish MUST use the override root from the resolved struct (dual-return shape contract)")
}

func TestResolveDestination_VoiceoverWithParentFolderID_BuildsSubpath(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "voiceover-sub-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	// Caller supplies an explicit Drive folder ID (e.g. via
	// cmd/admin resolve flow OR voiceover handler's prior
	// GetOrCreateFolder call). Project + language drive the
	// canonical {project}/{language} subfolder.
	override := "explicit-voiceover-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationVoiceover,
		LocalPath:      "/tmp/storia-boxe-it.mp3",
		Filename:       "storia-boxe-it.mp3",
		ProjectID:      "storia-boxe-it",
		Language:       "it-IT",
		ParentFolderID: override,
		// ConflictPolicy omitted → registry-driven default applies
		// (Voiceover is ConflictSkip per P1.1 mapping).
	})
	require.NoError(t, err)

	// (1) EnsureFolder MUST be called with the EXPLICIT override as the
	//     parent (NOT the registry vo-root "vo-root") — the PR-VO-SUBFOLDER
	//     invariant: the override wins for the parent, the PathBuilder
	//     still owns the canonical subpath segments.
	require.Len(t, folders.ensureCalls, 1,
		"voiceover with ParentFolderID MUST trigger exactly one EnsureFolder call (PR-VO-SUBFOLDER invariant)")
	require.Equal(t, override, folders.ensureCalls[0].parent,
		"EnsureFolder MUST be called with the explicit ParentFolderID as parent — NOT the registry vo-root")
	require.Equal(t, []string{"storia-boxe-it", "it-IT"}, folders.ensureCalls[0].segments,
		"EnsureFolder MUST be called with the canonical voiceover subpath [{project},{language}] — SafeFolderName preserves alphanum + hyphen")

	// (2) Upload landed in the sub-folder returned by EnsureFolder (NOT
	//     the override root directly).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "voiceover-sub-folder-id", files.uploadCalls[0].folderID,
		"Upload MUST land in the canonical sub-folder returned by EnsureFolder")
}

func TestResolveDestination_PathBuilderFailsWithOverride_FallsBack(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}

	// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE (July 2026): the
	// fallback diagnostic moved from Warn to Debug (the explicit
	// errors.Is sentinel at the call-site surface is now the
	// canonical diagnostic; the Debug log is the explicit call-site
	// ack that the swallow took place, NOT the primary failure
	// surface). The observer uses DebugLevel to capture the ack.
	core, recorded := observer.New(zapcore.DebugLevel)
	log := zap.New(core)

	pub, err := NewPublisher(reg, folders, files, log)
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip.mp4",
		ParentFolderID: override,
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
		// ConflictPolicy explicit Overwrite so the registry's
		// ConflictSkip default is bypassed (test focuses on the
		// PathBuilder branch, not the policy branch).
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err,
		"PathBuilder failure with explicit override MUST NOT propagate — backward-compat fallback to direct-to-root")

	// (1) EnsureFolder MUST NOT be called (no segments → direct upload).
	require.Empty(t, folders.ensureCalls,
		"EnsureFolder MUST NOT be called when PathBuilder fails + ParentFolderID is set (direct-to-root fallback)")

	// (2) Upload landed directly in the override root.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Upload MUST land in the explicit override root (direct-to-root fallback, no subfolder created)")

	// (3) Debug-level explicit-ack diagnostic surfaced the fallback so
	//     the operator sees the metadata gap in logs (NOT silent). The
	//     message text is the canonical 'incomplete subpath tolerated'
	//     ack that the call-site uses after errors.Is'ing
	//     ErrPathBuilderIncompleteForParent.
	require.NotEmpty(t, recorded.All(), "expected at least one log entry on PathBuilder failure with override — got none")
	debugFound := false
	for _, entry := range recorded.All() {
		if strings.Contains(entry.Message, "incomplete subpath tolerated because override was set") {
			debugFound = true
			break
		}
	}
	require.True(t, debugFound,
		"expected Debug log with 'incomplete subpath tolerated because override was set' — got %v (PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE call-site ack contract)",
		recorded.All())
}

func TestResolveDestination_PathBuilderFailsNoOverride_ReturnsError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip.mp4",
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject omitted → YouTubeClipPath returns "subject (video ID) is required".
		// ParentFolderID omitted (zero value).
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.Error(t, err,
		"PathBuilder failure with NO override MUST propagate to caller — the registry default would otherwise hide a metadata gap")
	require.Contains(t, err.Error(), "subject",
		"Error must surface PathBuilder's underlying 'subject (video ID) is required' — wrapping preserved so callers can errors.Is/As on it")
	require.Contains(t, err.Error(), "build path",
		"Error must include the publisher's 'delivery: build path for %q: %w' prefix so the canonical seam is grep-able in logs")
}

func TestErrPathBuilderIncompleteForParent_Sentinel(t *testing.T) {
	var _ error = ErrPathBuilderIncompleteForParent // compile-time pin

	// (a) Bare sentinel: errors.Is matches the error itself.
	require.ErrorIs(t, ErrPathBuilderIncompleteForParent, ErrPathBuilderIncompleteForParent,
		"bare sentinel MUST errors.Is match itself")

	// (b1) Production dual-%w fmt.Errorf wrap (canonical in resolveDestination).
	//      underlyingCause is held for the errors.Is identity probe at (c).
	underlyingCause := fmt.Errorf("group is required")
	wrapped := fmt.Errorf("delivery: PathBuilder failed under ParentFolderID (cause: %w): %w",
		underlyingCause, ErrPathBuilderIncompleteForParent)

	// (b2) errors.Is recovers the sentinel via wrap-chain walk.
	require.ErrorIs(t, wrapped, ErrPathBuilderIncompleteForParent,
		"dual-%w fmt.Errorf must preserve the sentinel for errors.Is (typed-chain preservation contract)")

	// (c) Typed-recovery: errors.Is recovers the underlying cause via
	//     pointer-identity match against the SAME underlyingCause variable
	//     (== equality through the wrap chain). This is equivalent to
	//     errors.As(wrapped, &concreteErrorType) for purposes of the
	//     godlike/07 typed-recovery contract, without the *error target
	//     go vet rejection.
	require.ErrorIs(t, wrapped, underlyingCause,
		"dual-%w fmt.Errorf must preserve the underlying PathBuilder cause via errors.Is (typed-recovery contract — equivalent to errors.As for chain-preservation)")

	// (d) Underlying cause's message preserved in err.Error() for grep-ability.
	require.Contains(t, wrapped.Error(), "group is required",
		"dual-%w fmt.Errorf must preserve the underlying cause's message via the wrap chain (log/diagnostic surface)")

	// (e) Sentinel discriminator phrase preserved (stable against message rewording).
	require.Contains(t, wrapped.Error(), "ParentFolderID",
		"dual-%w fmt.Errorf must preserve the sentinel's discriminator phrase (ParentFolderID) for grep-able diagnostic surface")

	// (f) DOWNSTREAM-COMPAT ALIAS ONLY — errors.Join is equally valid for
	//     downstream consumers (godlike/06 SSOT does NOT forbid it; production
	//     uses dual-%w fmt.Errorf for the single-line-stderr benefit). The
	//     alias is documented here to prevent future agents from switching
	//     publisher.go to errors.Join and regressing to the 3-line stderr
	//     noise that breaks single-line log aggregators. DO NOT change
	//     publisher.go to use errors.Join — the dual-%w fmt.Errorf wrap is
	//     the canonical production idiom per godlike/07 single-line stderr
	//     criterion.
	joinedCause := fmt.Errorf("group is required")
	joined := errors.Join(joinedCause, ErrPathBuilderIncompleteForParent)
	require.ErrorIs(t, joined, ErrPathBuilderIncompleteForParent,
		"errors.Join downstream-compat: sentinel preserved for errors.Is")
	require.ErrorIs(t, joined, joinedCause,
		"errors.Join downstream-compat: underlying cause preserved for errors.Is (typed-recovery alias)")

	// (g) Negative control: unrelated sentinel does NOT match.
	otherSentinel := errors.New("drive: unrelated sentinel")
	require.NotErrorIs(t, wrapped, otherSentinel,
		"dual-%w wrapped sentinel MUST NOT match unrelated sentinels (errors.Is isolation)")
}

func TestPublisher_NormalizeFilename(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	// Path traversal in filename should be sanitised.
	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "../evil.txt",
		Group:       "test",
		Subject:     "abc",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.FileID)
	// The filename should have been sanitised (no ".." in it).
	require.Len(t, files.uploadCalls, 1)
	require.NotContains(t, files.uploadCalls[0].filename, "..")
}

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
			Destination:    delivery.DestinationVoiceover,
			LocalPath:      "/tmp/" + lc.filename,
			Filename:       lc.filename,
			ProjectID:      project,
			Language:       lc.lang,
			ParentFolderID: override,
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
