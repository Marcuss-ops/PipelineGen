package drive

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
)

// ── Test doubles ───────────────────────────────────────────────────────

type fakeFolderManager struct {
	// ensureCalls records each EnsureFolder invocation.
	ensureCalls []folderCall
	// result is the leaf folder ID returned by EnsureFolder.
	result string
	// err, if set, is returned by EnsureFolder.
	err error
}

type folderCall struct {
	parent   string
	segments []string
}

func (f *fakeFolderManager) EnsureFolder(_ context.Context, parent string, segments ...string) (string, error) {
	f.ensureCalls = append(f.ensureCalls, folderCall{parent: parent, segments: segments})
	if f.err != nil {
		return "", f.err
	}
	if f.result == "" {
		return "fake-folder-id", nil
	}
	return f.result, nil
}

// fakeFileUploader impl Pattern-0 FileUploaderPort (P0 #1, June 2026):
// the only method on the port is PutFile; the fake records incoming
// PutFileRequest values into uploadCalls and returns a configured
// PutAction so tests for the overwrite/skip/rename branches can pin
// the exact outcome the uploader would have produced.
type fakeFileUploader struct {
	// uploadCalls records each PutFile invocation. Field name preserved
	// from the legacy UploadFileWithDescription fake so existing tests
	// keep their assertion shape (files.uploadCalls[0].localPath etc.).
	uploadCalls []uploadCall
	// err, if set, is returned by PutFile.
	err error
	// putAction, if set, overrides the default PutActionCreated so
	// tests for the skip/rename branches can pin the exact action
	// the underlying uploader would have produced.
	putAction PutAction
}

type uploadCall struct {
	localPath   string
	folderID    string
	filename    string
	description string
	// policy (P0 #1, June 2026) is the ConflictPolicy the publisher
	// forward through PutFileRequest. Zero value == ConflictOverwrite
	// matches legacy behaviour, so legacy tests that omit the field
	// still see the same result shape.
	policy delivery.ConflictPolicy
}

func (f *fakeFileUploader) PutFile(_ context.Context, req PutFileRequest) (*PutFileResult, error) {
	f.uploadCalls = append(f.uploadCalls, uploadCall{
		localPath:   req.LocalPath,
		folderID:    req.FolderID,
		filename:    req.Filename,
		description: req.Description,
		policy:      req.ConflictPolicy,
	})
	if f.err != nil {
		return nil, f.err
	}
	action := f.putAction
	if action == "" {
		action = PutActionCreated
	}
	return &PutFileResult{
		FileID:       "fake-file-id",
		WebViewLink:  "https://drive.google.com/file/d/fake-file-id",
		DownloadLink: "https://drive.google.com/uc?id=fake-file-id",
		Action:       action,
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func testRegistry() *delivery.DestinationRegistry {
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder:       "media-root",
			ClipsRootFolder:       "clips-root",
			ArtlistRootFolder:     "artlist-root",
			StockRootFolder:       "stock-root",
			ImagesRootFolder:      "images-root",
			VoiceoverRootFolder:   "vo-root",
			BooksRootFolder:       "books-root",
			ScriptsRootFolder:     "scripts-root",
			SoundEffectsRootFolder: "sfx-root",
		},
	}
	return delivery.NewDestinationRegistry(cfg)
}

// degenerateEmptyPathBuilder is the canonical test-only PathBuilder that
// silently returns an empty segment slice. It mirrors what a malformed
// or even maliciously crafted policy could do at runtime, and only
// RequireSubpath=true callers can catch it. Used by P0 #2 regression
// tests to verify that resolveDestination (and therefore both Publish
// AND ResolveFolder) rejects it symmetrically.
func degenerateEmptyPathBuilder(_ delivery.PublishRequest) ([]string, error) {
	return []string{}, nil
}

// ── Tests ──────────────────────────────────────────────────────────────

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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group and Subject are empty → PathBuilder returns error
	})
	require.Error(t, err, "should reject when PathBuilder fails due to missing fields")
	require.Contains(t, err.Error(), "group")
}

func TestPublisher_UnknownDestinationRejected(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

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
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve drive path")
}

// ── P0 #1 tests (June 2026) — ConflictPolicy plumbing ─────────────────

// TestPublisher_PublishForwardsConflictPolicy_ZoomValue pins the
// end-to-end ConflictPolicy forward: the Publisher must pass
// req.ConflictPolicy through publishers' PutFileRequest and the
// underlying uploader returns PutActionCreated by default (matching
// legacy zero-value behaviour for callers that omitted the field).
func TestPublisher_PublishForwardsConflictPolicy_ZeroValue(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
		// ConflictPolicy omitted — zero value should default to
		// ConflictOverwrite, exactly the legacy semantics.
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy,
		"zero-value ConflictPolicy must flow through as ConflictOverwrite (default legacy behaviour)")
}

func TestPublisher_PublishForwardsConflictPolicy_Overwrite(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionUpdated}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy)
}

func TestPublisher_PublishForwardsConflictPolicy_Skip(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionSkipped}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictSkip,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy)
}

func TestPublisher_PublishForwardsConflictPolicy_Rename(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionRenamed}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/video.mp4",
		Filename:       "video.mp4",
		Group:          "test",
		Subject:        "abc",
		ConflictPolicy: delivery.ConflictRename,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictRename, files.uploadCalls[0].policy)
}

// TestPublisher_PublishRejectsRequireSubpath is the P0 #2 paired test
// for the Publish side of the symmetric enforcement. Uses a degenerate
// registry where RequireSubpath=true and the PathBuilder returns an
// empty segment slice. Without the centralised resolveDestination
// helper, this would have been caught inside Publish's Step 3; with
// the refactor, the check lives in the helper so this test verifies
// the Publish path still rejects (which it does, via the helper).
func TestPublisher_PublishRejectsRequireSubpath(t *testing.T) {
	reg := delivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID:   "clips-root",
			PathBuilder:    degenerateEmptyPathBuilder,
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	_, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group/Subject omitted on purpose: degenerateEmptyPathBuilder
		// accepts them and returns []string{}, forcing the helper to
		// trigger the RequireSubpath check.
	})
	require.Error(t, err, "Publish must reject when PathBuilder returns []string{} and RequireSubpath=true")
	require.Contains(t, err.Error(), "forbidden", "Publish error must come from the RequireSubpath check")
}

// TestPublisher_ResolveFolder_HonorsRequireSubpath is the P0 #2 regression
// catch for the symmetric enforcement. Before P0 #2, ResolveFolder
// skipped the RequireSubpath check entirely (it had a near-duplicate
// of Steps 1-4 but dropped Step 3), so this test would have FAILED:
// ResolveFolder would have returned the rootFolderID without error,
// even though the SAME request would have been rejected by Publish.
//
// With P0 #2 both Publish and ResolveFolder go through
// resolveDestination so the check fires symmetrically. This test
// would silently PASS today but still serves as a guard against
// future drift — if a developer reverts the refactor and ResolveFolder
// gets a duplicated Steps-1-4 block again, this test catches them.
func TestPublisher_ResolveFolder_HonorsRequireSubpath(t *testing.T) {
	reg := delivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID:   "clips-root",
			PathBuilder:    degenerateEmptyPathBuilder,
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	got, err := pub.ResolveFolder(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		// Group/Subject omitted — degenerateEmptyPathBuilder returns
		// []string{} on purpose so the helper triggers the check.
	})
	require.Error(t, err, "ResolveFolder must reject when PathBuilder returns []string{} and RequireSubpath=true (was the Pre-P0#2 bypass — must NOT regress)")
	require.Contains(t, err.Error(), "forbidden", "ResolveFolder error must come from the RequireSubpath check, not from a uploader/folder error")
	require.Empty(t, got, "ResolveFolder must return empty folder ID on rejection")

	// Sanity: the symmetric Publish call must ALSO reject with the
	// SAME error class. This proves both paths flow through the same
	// helper and the user-facing surface is now consistent.
	_, publishErr := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
	})
	require.Error(t, publishErr, "Publish must reject symmetrically with ResolveFolder")
	require.Contains(t, publishErr.Error(), "forbidden")

	// Sanity: EnsureFolder must NOT have been called. The check fires
	// before folder hierarchy creation, so no Drive writes can leak.
	require.Empty(t, folders.ensureCalls, "ResolveFolder must reject BEFORE EnsureFolder (no Drive writes on rejection)")
	require.Empty(t, files.uploadCalls, "ResolveFolder must reject BEFORE PutFile (no upload considerations)")
}

// TestPublisher_ResolveFolder_SuccessWhenSubpathProvided is the positive
// counterpart. With a degenerate registry but a real PathBuilder that
// returns segments, both Publish and ResolveFolder should succeed.
func TestPublisher_ResolveFolder_SuccessWhenSubpathProvided(t *testing.T) {
	reg := delivery.NewDestinationRegistryWithPolicies(map[delivery.DestinationKey]delivery.DestinationPolicy{
		delivery.DestinationYouTubeClip: {
			RootFolderID: "clips-root",
			PathBuilder: func(_ delivery.PublishRequest) ([]string, error) {
				return []string{"NBA News", "video-xyz"}, nil
			},
			RequireSubpath: true,
		},
	})
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{}
	pub := NewPublisher(reg, folders, files, zap.NewNop())

	got, err := pub.ResolveFolder(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
	})
	require.NoError(t, err, "ResolveFolder should succeed when PathBuilder returns non-empty segments")
	require.Equal(t, "video-folder-id", got, "ResolveFolder should return the leaf folder ID from EnsureFolder")
	require.Len(t, folders.ensureCalls, 1)
	require.Equal(t, []string{"NBA News", "video-xyz"}, folders.ensureCalls[0].segments)
}
