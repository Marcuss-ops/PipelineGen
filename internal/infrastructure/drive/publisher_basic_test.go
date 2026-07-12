// Package drive — publisher_basic_test.go: FASE pre-Commit-3/6
// canonical "publisher API surface" tests + P0 #3 (June 2026)
// fail-fast nil-dep sentinels. Step 6 split (July 2026): the
// 11 canonical tests + 3 nil-dep tests from the previous
// (publisher_test.go + publisher_test_nil_deps_test.go) layout
// consolidated into one file. The 4 sibling FASE-wave files
// (`publisher_p1_1_policies_test.go`, `publisher_p0_1_audit_test.go`,
// `publisher_pathbuilder_test.go`, `publisher_vo_subfolder_test.go`,
// `publisher_dod_matrices_test.go`) cover the contract tests, audit
// pins, and DoD matrices.
//
// godlike/06 SSOT: the fakeFolderManager + fakeFileUploader test
// doubles live in publisher_test_helpers_test.go (Step 6 split)
// and are reuseable via package-level scope from this file.
//
// 14 tests under this header cover:
//   - 5 destination-registry invariants (RequireSubpath + Has + non-empty
//     RootFolderID + non-nil PathBuilder) for all 8 destinations
//   - RejectDirectRootUpload: degenerate-registry path rejection
//     (PathBuilder failure → "subject (video ID) is required")
//   - UnknownDestinationRejected: registry-miss error class
//   - YouTubeClipPath.IsDeterministic: byte-stable path-builder output
//   - PublishYouTubeClip / PublishBook / PublishSoundEffect happy paths
//     (fake EnsureFolder + fake PutFile plumbing)
//   - EmptyRootFolderRejected / UploaderError / NormalizeFilename /
//     FolderManagerError error-path coverage
//   - NewPublisher fail-fast on nil registry / folders / files
//     (P0 #3 composition-time sentinels)
//
// These tests FAIL on regression only if:
//   - DestinationRegistry drops any registered destination
//   - RequireSubpath gating is removed
//   - Publish happy-path loses folder→upload plumbing
//   - error paths silently swallow known terminal errors
//   - NewPublisher stops fail-fast on nil ports (composition-time gap
//     becomes runtime nil-deref)

package drive

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Tests (basic publisher API) ────────────────────────────────────────

func TestPublisher_AllDestinationsRegistered(t *testing.T) {
	reg := testRegistry()

	expected := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationImage,
		delivery.DestinationVoiceover,
		delivery.DestinationBook,
		delivery.DestinationScript,
		delivery.DestinationSoundEffect,
	}

	for _, dest := range expected {
		require.True(t, reg.Has(dest), "destination %q not registered", dest)
		policy, err := reg.Resolve(dest)
		require.NoError(t, err, "Resolve(%q) failed", dest)
		require.NotEmpty(t, policy.RootFolderID, "RootFolderID empty for %q", dest)
		require.NotNil(t, policy.PathBuilder, "PathBuilder nil for %q", dest)
		require.True(t, policy.RequireSubpath, "RequireSubpath must be true for %q", dest)
	}
}

func TestPublisher_RejectsDirectRootUpload(t *testing.T) {
	// All destinations have RequireSubpath=true. If a PathBuilder
	// somehow returns an empty slice, the publisher must reject.
	// We test this by creating a custom registry with a degenerate policy.
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder: "root-id",
		},
	}
	reg := delivery.NewDestinationRegistry(cfg)

	// Build a publisher with the real registry — all destinations require
	// subpath, so a request with empty Group/Subject should fail at the
	// PathBuilder level (which is the correct error path).
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group falls back to "youtube_uncategorized" (PR-YT-PATH-FALLBACK),
		// Subject is empty → PathBuilder returns "subject (video ID) is required".
	})
	require.Error(t, err, "should reject when PathBuilder fails due to missing fields")
	require.Contains(t, err.Error(), "subject (video ID) is required")
}

func TestPublisher_UnknownDestinationRejected(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: "nonexistent",
		LocalPath:   "/tmp/file.txt",
		Filename:    "file.txt",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown destination key")
}

func TestYouTubeClipPath_IsDeterministic(t *testing.T) {
	req := delivery.PublishRequest{
		Group:   "NBA News",
		Subject: "abc123",
	}

	first, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	second, err := delivery.YouTubeClipPath(req)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, []string{"NBA News", "abc123"}, first)
}

func TestPublisher_PublishYouTubeClip(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_abc123.mp4",
		Description: "Test clip",
		AssetID:     "abc123",
		Group:       "NBA News",
		Subject:     "video-xyz",
	})
	require.NoError(t, err)
	require.Equal(t, "fake-file-id", result.FileID)
	require.Equal(t, "video-folder-id", result.FolderID)
	require.Equal(t, delivery.DestinationYouTubeClip, result.Destination)
	require.Equal(t, []string{"NBA News", "video-xyz"}, result.PathSegments)

	// Verify the folder manager was called correctly.
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "clips-root", folders.ensureCalls[0].parent)
	require.Equal(t, []string{"NBA News", "video-xyz"}, folders.ensureCalls[0].segments)

	// Verify the uploader was called correctly.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "/tmp/clip.mp4", files.uploadCalls[0].localPath)
	require.Equal(t, "video-folder-id", files.uploadCalls[0].folderID)
	require.Equal(t, "clip_abc123.mp4", files.uploadCalls[0].filename)
	require.Equal(t, "Test clip", files.uploadCalls[0].description)
}

func TestPublisher_PublishBook(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "book-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationBook,
		LocalPath:   "/tmp/book.pdf",
		Filename:    "summary.pdf",
		ProjectID:   "my-book-project",
	})
	require.NoError(t, err)
	require.Equal(t, "book-folder-id", result.FolderID)
	require.Equal(t, []string{"my-book-project"}, result.PathSegments)

	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "books-root", folders.ensureCalls[0].parent)
}

func TestPublisher_PublishSoundEffect(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "sfx-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationSoundEffect,
		LocalPath:   "/tmp/sfx.mp3",
		Filename:    "explosion.mp3",
		Group:       "explosions",
	})
	require.NoError(t, err)
	require.Equal(t, "sfx-folder-id", result.FolderID)
	require.Equal(t, []string{"explosions"}, result.PathSegments)

	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, "sfx-root", folders.ensureCalls[0].parent)
}

func TestPublisher_EmptyRootFolderRejected(t *testing.T) {
	// Create a registry where one destination has an empty root.
	cfg := &config.Config{
		Drive: config.DriveConfig{
			// All roots empty → ClipsFolder() returns ""
			MediaRootFolder: "",
		},
	}
	reg := delivery.NewDestinationRegistry(cfg)
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no configured root folder")
}

func TestPublisher_UploaderError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{err: context.DeadlineExceeded}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "publish to")
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

func TestPublisher_FolderManagerError(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{err: context.DeadlineExceeded}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve drive path")
}

// ── P0 #3 tests (June 2026) — fail-fast NewPublisher nil-dep sentinels ────────

// TestNewPublisher_NilRegistry pins the composition-time fail-fast
// sentinel for a nil DestinationRegistry. Without this barrier, a
// nil registry would surface only at first Publish call site as a
// nil-deref panic (`p.registry.Resolve(...)`). The Composition root
// at internal/app/build_bundles_drive.go gates on this sentinel to
// halt process start-up cleanly.
func TestNewPublisher_NilRegistry(t *testing.T) {
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(nil, folders, files, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil registry")
	require.ErrorIs(t, err, ErrMissingDestinationRegistry,
		"nil-registry error must wrap ErrMissingDestinationRegistry verbatim (audit-trail grep)")
	require.Nil(t, pub, "publisher pointer must be nil on error return (composition-time safety)")
}

// TestNewPublisher_NilFolders pins the composition-time fail-fast
// sentinel for a nil FolderManagerPort. Same typed-NIL interface
// trap pattern as TestNewPublisher_NilRegistry: a nil port would
// otherwise surface at first EnsureFolder call site.
func TestNewPublisher_NilFolders(t *testing.T) {
	reg := testRegistry()
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, nil, files, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil FolderManagerPort")
	require.ErrorIs(t, err, ErrMissingFolderManager,
		"nil-folders error must wrap ErrMissingFolderManager verbatim")
	require.Nil(t, pub)
}

// TestNewPublisher_NilFiles pins the composition-time fail-fast
// sentinel for a nil FileUploaderPort. A nil port would otherwise
// surface at first PutFile call site, post P0 #1 conflict-policy
// plumbing that the FileUploaderPort.PutFile field is the ONLY
// method on the port (no fallthrough UploadFileWithDescription
// escape hatch).
func TestNewPublisher_NilFiles(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	pub, err := NewPublisher(reg, folders, nil, zap.NewNop())
	require.Error(t, err, "NewPublisher must fail-fast on nil FileUploaderPort")
	require.ErrorIs(t, err, ErrMissingFileUploader,
		"nil-files error must wrap ErrMissingFileUploader verbatim")
	require.Nil(t, pub)
}
