package drive

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"strings"
	"testing"
)

// ── Test doubles ───────────────────────────────────────────────────────

type fakeFolderManager struct {
	// ensureCalls records each EnsureFolder invocation.
	ensureCalls []folderCall
	// result is the leaf folder ID returned by EnsureFolder.
	result string
	// err, if set, is returned by EnsureFolder.
	err error
	// probeCalls records each ProbeFolderAccess invocation. P1.3 (July 2026)
	// adds this tracking so the publisher tests can assert the validator
	// surface (kept lightweight — only the rootID matters, the SDK
	// response is irrelevant at this layer).
	probeCalls []string
	// probeErr, if set, is returned by ProbeFolderAccess. With
	// probeErrFn non-nil, probeErr is ignored and probeErrFn is
	// called per rootID (used for selective tests where some roots
	// fail and others pass).
	probeErr   error
	probeErrFn func(rootID string) error
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

// ProbeFolderAccess implements the P1.3 FolderManagerPort extension.
// Records the rootID for later assertion; returns probeErr (or the
// probeErrFn result) when set, nil otherwise. Mirrors the
// production DriveFolderManagerAdapter semantics: empty rootID is
// rejected with an error, not silently passed through.
func (f *fakeFolderManager) ProbeFolderAccess(_ context.Context, rootID string) error {
	f.probeCalls = append(f.probeCalls, rootID)
	if strings.TrimSpace(rootID) == "" {
		return fmt.Errorf("probeFolderAccess: root ID is empty")
	}
	if f.probeErrFn != nil {
		return f.probeErrFn(rootID)
	}
	return f.probeErr
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
	// md5Checksum, if set, is returned verbatim by PutFile as the
	// MD5Checksum field of PutFileResult. Drives the P0 #9 test that
	// verifies Publisher.Publish propagates MD5Checksum through to
	// delivery.PublishResult without reconstruction.
	md5Checksum string
	// existingFilenames, if non-nil and a PutFile call's filename is
	// present in the set, simulates a Drive-side existing-match and
	// routes the PutAction per req.ConflictPolicy (mimicking the
	// production Uploader.doPutFile routing table). When the filename
	// is NOT in the set (or existingFilenames is nil), the action
	// falls through to putAction/PutActionCreated as before.
	//
	// Drives the P0 #1 audit pin tests #5 + #6 (ConflictSkip /
	// ConflictRename on existing-match) without requiring a mock
	// Google Drive API client.
	existingFilenames map[string]bool
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
	// routedAction (P0 #1, June 2026) is the PutAction that the fake's
	// routing table decided for this specific call (AFTER the
	// existingFilenames branch). Per-call capture — not a top-level
	// mutable on the fake — so multi-call tests can assert per index
	// (e.g. files.uploadCalls[0].routedAction) without last-write-wins
	// footguns. Populated on BOTH success AND error paths: the routing
	// decision is independent of whether the underlying PutFile later
	// failed (e.g. f.err set) — assertors that need the post-error
	// suppression pattern must NOT rely on routedAction being zero.
	routedAction PutAction
}

func (f *fakeFileUploader) PutFile(_ context.Context, req PutFileRequest) (*PutFileResult, error) {
	// Behavioral routing mode (P0 #1 audit pins #5 + #6): when
	// existingFilenames is non-nil and contains the filename being
	// PUT, route the outcome through the same env-decision table as
	// Uploader.doPutFile. The actual Drive SDK call shapes are local
	// to the production uploader; this fake pins the Publisher-side
	// contract that a PutAction of Skipped/Renamed/Updated flows
	// through to delivery.PublishAction correctly.
	action := f.putAction
	if action == "" {
		action = PutActionCreated
	}
	if f.existingFilenames != nil && f.existingFilenames[req.Filename] {
		switch req.ConflictPolicy {
		case delivery.ConflictSkip:
			action = PutActionSkipped
		case delivery.ConflictRename:
			action = PutActionRenamed
		case delivery.ConflictOverwrite:
			action = PutActionUpdated
		default:
			// ConflictPolicyUnset (zero-value of the enum, P1.1
			// sentinel). Publisher.Step0 resolves Unset to a
			// registry-driven default BEFORE PutFile is called,
			// so the uploader seam in production never receives
			// the Unset sentinel. This default branch catches
			// direct PutFile invocations from test contexts only
			// (e.g. uploader_put_test.go exercising the legacy
			// doPutFile shape). Pre-P1.1 the routed action was
			// PutActionUpdated for the same reason: a missing
			// policy used to mean "overwrite" silently, so we
			// preserve that for direct-call tests rather than
			// introducing a behaviour drift.
			action = PutActionUpdated
		}
	}
	// Record the call WITH the routed action so callers can assert
	// per-index routedAction. Per-call capture (NOT a top-level field
	// on the fake) avoids last-write-wins footguns on multi-call tests.
	// On the error branch routedAction is "" — the routing decision
	// was suppressed, and tests that exercise the error path don't
	// pin routedAction (they assert the error message + policy field).
	f.uploadCalls = append(f.uploadCalls, uploadCall{
		localPath:    req.LocalPath,
		folderID:     req.FolderID,
		filename:     req.Filename,
		description:  req.Description,
		policy:       req.ConflictPolicy,
		routedAction: action,
	})
	if f.err != nil {
		return nil, f.err
	}
	return &PutFileResult{
		FileID:       "fake-file-id",
		WebViewLink:  "https://drive.google.com/file/d/fake-file-id",
		DownloadLink: "https://drive.google.com/uc?id=fake-file-id",
		MD5Checksum:  f.md5Checksum,
		Action:       action,
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func testRegistry() *delivery.DestinationRegistry {
	cfg := &config.Config{
		Drive: config.DriveConfig{
			MediaRootFolder:        "media-root",
			ClipsRootFolder:        "clips-root",
			ArtlistRootFolder:      "artlist-root",
			StockRootFolder:        "stock-root",
			ImagesRootFolder:       "images-root",
			VoiceoverRootFolder:    "vo-root",
			BooksRootFolder:        "books-root",
			ScriptsRootFolder:      "scripts-root",
			SoundEffectsRootFolder: "sfx-root",
		},
	}
	return delivery.NewDestinationRegistry(cfg)
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

// ── P0 #3 tests (June 2026) — fail-fast NewPublisher nil-dep sentinels ───────

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

// ── P1.1 tests (July 2026) — ConflictPolicy plumbing ──────────────────
//
// P1.1 replaces the legacy zero = ConflictOverwrite silent fallback:
// the publisher now consults DestinationRegistry's per-destination
// ConflictPolicy default when req.ConflictPolicy == 0 (the "caller
// didn't pick" signal). Explicit req.ConflictPolicy values
// (Skip / Overwrite / Rename) are honoured verbatim.

// TestPublisher_Publish_RegistryDrivesZero_P1_1 pins the P1.1 contract:
// when req.ConflictPolicy == 0, the publisher reads the registry's
// per-destination default and applies it. YouTubeClip is ConflictSkip
// in the canonical registry (immutable uploaded clip) so a caller
// that forgot to pick a policy MUST NOT silently overwrite an
// existing matching Drive file — the pre-P1.1 silent-ConflictOverwrite
// footgun is gone.
//
// This replaces the legacy TestPublisher_PublishForwardsConflictPolicy_ZeroValue
// test, which had pinned the dangerous behaviour (zero → Overwrite).
func TestPublisher_Publish_RegistryDrivesZero_P1_1(t *testing.T) {
	reg := testRegistry()
	// YouTubeClip is registered with ConflictSkip in the canonical
	// registry builder. Verify the registry-side contract first so a
	// future drift in the constructor is caught before the publisher
	// assertion below.
	policy, err := reg.Resolve(delivery.DestinationYouTubeClip)
	require.NoError(t, err)
	require.Equal(t, delivery.ConflictSkip, policy.ConflictPolicy,
		"YouTubeClip registry default MUST be ConflictSkip (immutable uploaded clip) — P1.1 invariant")

	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
		// ConflictPolicy omitted (zero) — the publisher MUST
		// consult the registry and forward ConflictSkip, NOT the
		// pre-P1.1 ConflictOverwrite fallback.
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy,
		"zero-value req.ConflictPolicy MUST resolve to the registry's per-destination default (ConflictSkip for YouTubeClip) — pre-P1.1 ConflictOverwrite fallback is gone")
}

// TestPublisher_Publish_ExplicitOverridesRegistry_P1_1 pins the
// inverse direction: explicit req.ConflictPolicy wins over the
// registry default. A caller that wants ConflictOverwrite on a
// normally-Skip destination (e.g. an explicit admin reupload on a
// YouTubeClip) MUST be able to opt out without touching the registry.
//
// This keeps the door open for the historical "admin / migration /
// debug override" use case without re-introducing the silent-zero
// footgun.
func TestPublisher_Publish_ExplicitOverridesRegistry_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip, // registry default = ConflictSkip
		LocalPath:   "/tmp/video.mp4",
		Filename:    "video.mp4",
		Group:       "test",
		Subject:     "abc",
		// Explicit ConflictOverwrite — must NOT be overridden by
		// the registry's ConflictSkip default.
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy,
		"explicit req.ConflictPolicy MUST win over the registry default (P1.1 — explicit override surface)")
}

// TestRegistry_ConflictPolicyPerDestination_P1_1 pins the per-key
// ConflictPolicy mapping on the registry side so a future drift in
// the constructor (e.g. accidentally setting Image to Overwrite)
// surfaces as a failing test, not a silent overwrite in production.
//
// Per-destination mapping rationale:
//
//	YouTube / Artlist / Stock / Image / Voiceover / SoundEffect
//	  → ConflictSkip (immutable / versioned assets)
//	Book / Script / Document
//	  → ConflictOverwrite (regenerable, latest-version-wins outputs)
func TestRegistry_ConflictPolicyPerDestination_P1_1(t *testing.T) {
	reg := testRegistry()

	skipDestinations := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationVoiceover,
		delivery.DestinationSoundEffect,
	}
	for _, dest := range skipDestinations {
		dest := dest
		t.Run(string(dest)+"/skip", func(t *testing.T) {
			policy, err := reg.Resolve(dest)
			require.NoError(t, err, "missing policy for %q", dest)
			require.Equal(t, delivery.ConflictSkip, policy.ConflictPolicy,
				"%q is an immutable / versioned asset — registry default MUST be ConflictSkip (P1.1 invariant)", dest)
		})
	}

	t.Run(string(delivery.DestinationImage)+"/skip-by-hash", func(t *testing.T) {
		policy, err := reg.Resolve(delivery.DestinationImage)
		require.NoError(t, err, "missing policy for %q", delivery.DestinationImage)
		require.Equal(t, delivery.ConflictSkipByHash, policy.ConflictPolicy,
			"%q must default to ConflictSkipByHash (content-hash dedupe)", delivery.DestinationImage)
	})

	overwriteDestinations := []delivery.DestinationKey{
		delivery.DestinationBook,
		delivery.DestinationScript,
		delivery.DestinationDocument,
	}
	for _, dest := range overwriteDestinations {
		dest := dest
		t.Run(string(dest)+"/overwrite", func(t *testing.T) {
			policy, err := reg.Resolve(dest)
			require.NoError(t, err, "missing policy for %q", dest)
			require.Equal(t, delivery.ConflictOverwrite, policy.ConflictPolicy,
				"%q is a regenerable output — registry default MUST be ConflictOverwrite (P1.1 invariant)", dest)
		})
	}
}

// TestPublisher_Publish_RegenerableDestZeroIsOverwrite_P1_1 pins the
// cross-check: zero req.ConflictPolicy against a ConflictOverwrite
// destination (e.g. Book) resolves to ConflictOverwrite, NOT to zero
// or Unknown. This guards against a future drift where the registry
// lookup returns an empty ConflictPolicy by accident — the publisher
// MUST forward the registry value verbatim, and the registry value
// for regenerable destinations MUST be ConflictOverwrite.
func TestPublisher_Publish_RegenerableDestZeroIsOverwrite_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "book-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationBook, // regenerable → ConflictOverwrite
		LocalPath:   "/tmp/summary.pdf",
		Filename:    "summary.pdf",
		ProjectID:   "my-book-project",
		// ConflictPolicy omitted — publisher MUST resolve from
		// registry and forward ConflictOverwrite.
	})
	require.NoError(t, err)
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, delivery.ConflictOverwrite, files.uploadCalls[0].policy,
		"Book zero-passthrough MUST resolve to ConflictOverwrite via registry (P1.1 cross-check)")
}

// TestPublisher_Publish_UnknownDestinationZeroStaysTypedError_P1_1
// pins the error surface: when req.ConflictPolicy == 0 AND the
// destination is unknown, the publisher MUST surface the typed
// "unknown destination key" error from registry.Resolve — NOT a
// silent fall-through to ConflictOverwrite. The lookup happens BEFORE
// resolveDestination so an unknown-destination error cannot be masked
// by the registry-driven default flow.
func TestPublisher_Publish_UnknownDestinationZeroStaysTypedError_P1_1(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: "nonexistent_destination_key",
		LocalPath:   "/tmp/file",
		Filename:    "file",
		// ConflictPolicy omitted.
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown destination key",
		"P1.1 zero-passthrough MUST surface 'unknown destination' verbatim — NOT silently overwrite")
}

func TestPublisher_PublishForwardsConflictPolicy_Overwrite(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "folder-id"}
	files := &fakeFileUploader{putAction: PutActionUpdated}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
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
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
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
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
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

// ── P0 #9 tests (June 2026) — PublishResult enrichment ─────────────
//
// Pre-P0-#9 the publisher dropped DownloadLink / MD5Checksum / Action
// from the underlying PutFileResult, forcing every consumer to
// reconstruct the download URL via string interpolation. The test
// below pins the contract: PublishResult carries all four metadata
// fields verbatim from PutFileResult and the resolved PathSegments,
// so no consumer can fall back to reconstruction without first
// deleting this test or breaking it.

// TestPublisher_PublishEnrichesPublishResult_F1_5_P0_9 pins the P0
// #9 surface: PublishResult must expose DownloadLink, MD5Checksum,
// FolderPath, and a translated Action so no consumer reconstructs
// the download URL via string interpolation. Uses a non-default
// PutActionUpdated so the Action translation is observably distinct
// from the zero value.
func TestPublisher_PublishEnrichesPublishResult_F1_5_P0_9(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		putAction:   PutActionUpdated, // non-default to observe translation
		md5Checksum: "abc123def456",   // non-empty to observe propagation
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   "/tmp/clip.mp4",
		Filename:    "clip_abc.mp4",
		Group:       "NBA News",
		Subject:     "video-xyz",
	})
	require.NoError(t, err)

	// (1) DownloadLink propagated verbatim from PutFileResult —
	//     verifies the no-reconstruction contract.
	require.Equal(t, "https://drive.google.com/uc?id=fake-file-id", result.DownloadLink,
		"Publish must propagate PutFileResult.DownloadLink verbatim — consumers MUST never reconstruct via uc?id=")

	// (2) MD5Checksum propagated verbatim — closes the re-FindFileByName
	//     surface that pre-P0-#9 callers used to recover the hash.
	require.Equal(t, "abc123def456", result.MD5Checksum,
		"Publish must propagate PutFileResult.MD5Checksum verbatim")

	// (3) Action translated at the delivery/drive boundary via the
	//     SAME method that Publish calls (single source of truth).
	require.Equal(t, pub.actionFor(PutActionUpdated), result.Action,
		"Publish must translate drive.PutActionUpdated → delivery.PublishActionUpdated at the boundary")
	require.NotEqual(t, delivery.PublishActionUnknown, result.Action,
		"PublishActionUnknown must NOT silently swallow a real action")

	// (4) FolderPath = strings.Join(PathSegments, "/") — the derived
	//     single-string view that consumers downstream want.
	require.Equal(t, "NBA News/video-xyz", result.FolderPath,
		"Publish must compute FolderPath as strings.Join(PathSegments, \"/\")")

	// (5) PathSegments remains authoritative: FolderPath and
	//     PathSegments coexist; PathSegments preserves order and
	//     is the canonical ordered view (FolderPath is derived).
	require.Equal(t, []string{"NBA News", "video-xyz"}, result.PathSegments,
		"PathSegments remains the authoritative ordered view; FolderPath is the derived display string")
}

// TestPublisher_TranslatePutAction_Table pins the boundary switch
// arm-by-arm, calling the SAME Publisher.actionFor method that
// production uses (single source of truth). Adding a future
// drive.PutAction constant without updating the switch surfaces as
// a failing test, not a silent fall-through to PublishActionUnknown.
func TestPublisher_TranslatePutAction_Table(t *testing.T) {
	reg := testRegistry()
	pub, err := NewPublisher(reg, &fakeFolderManager{}, &fakeFileUploader{}, zap.NewNop())
	require.NoError(t, err)

	cases := []struct {
		name     string
		input    PutAction
		expected delivery.PublishAction
	}{
		{"created", PutActionCreated, delivery.PublishActionCreated},
		{"updated", PutActionUpdated, delivery.PublishActionUpdated},
		{"skipped", PutActionSkipped, delivery.PublishActionSkipped},
		{"renamed", PutActionRenamed, delivery.PublishActionRenamed},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.expected, pub.actionFor(c.input),
				"drive.PutAction%q must translate to delivery.PublishAction%q at the boundary", c.input, c.expected)
		})
	}
}

// (Root-upload FolderPath empty + P0 #9 enriched-fields pin lives in
// publisher_policies_test.go alongside the other policy tests; it
// requires delivery.NewDestinationRegistryWithPolicies which is
// itself build-tag gated.)

// ── P0 #1 tests (June 2026) — audit pins #5 + #6 ──────────────────────
//
// Pre-P0-#1 ConflictPolicy was a dead enum — every caller passed the
// zero value and Uploader.UploadFileWithDescription always overwrote.
// F1.1 (commit 70f2b6c8) introduced the PutFileRequest/PutFileResult
// seam so policy + outcomes are explicit. The two tests below pin
// the end-to-end behavior at the Publisher translation layer:
// ConflictSkip + an existing-match on Drive must surface as
// PublishActionSkipped (audit pin #5 — "no upload happened"); and
// ConflictRename + existing-match must surface as
// PublishActionRenamed (audit pin #6 — "a new file gets created
// under the new name"). The fakeFileUploader's behavioral routing
// mode mimics Uploader.doPutFile's table without needing a mock
// Google Drive API client.

// TestPublisher_Publish_P0_1_ConflictSkip_ExistingMatch pins audit pin #5:
// when ConflictSkip propagates through Publisher and the underlying
// uploader reports an existing match (PutActionSkipped), the resulting
// PublishResult must surface PublishActionSkipped. The audit pin
// "no upload happened" is enforced at the Uploader layer by
// Uploader.doPutFile's ConflictSkip short-circuit (return existing
// metadata WITHOUT opening the local file); this publisher-layer
// pin proves the conflict outcome reaches the consumer without
// being reshaped or demoted to Created.
func TestPublisher_Publish_P0_1_ConflictSkip_ExistingMatch(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		existingFilenames: map[string]bool{"clip_abc.mp4": true},
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip_abc.mp4",
		Group:          "NBA News",
		Subject:        "video-xyz",
		ConflictPolicy: delivery.ConflictSkip,
	})
	require.NoError(t, err)

	// (1) Policy was forwarded to the Uploader seam correctly.
	require.Equal(t, delivery.ConflictSkip, files.uploadCalls[0].policy,
		"Publisher must forward ConflictSkip to PutFileRequest verbatim")

	// (2) Audit pin #5: the underlying Uploader routed the existing-match
	// through ConflictSkip → PutActionSkipped (no Drive write), and
	// Publisher translated that to PublishActionSkipped at the
	// delivery/drive boundary. End-to-end pin: result.Action is the
	// "no upload happened" signal that downstream ledger / dedupe /
	// no-op branches depend on.
	//
	// The fake's routed action is captured in lastAction by
	// fakeFileUploader.PutFile; the publisher's translation is captured
	// by pub.actionFor (single source of truth across prod and table
	// tests). Asserting both — and requiring that result.Action equals
	// the translation of lastAction — proves the chain end-to-end.
	require.Equal(t, PutActionSkipped, files.uploadCalls[0].routedAction,
		"behavioral fake must route to PutActionSkipped when filename exists + policy Skip — audit pin #5 closure")
	require.Equal(t, pub.actionFor(files.uploadCalls[0].routedAction), result.Action,
		"Publish must translate the routed PutAction to delivery.PublishAction at the boundary — no-reconstruction contract")
	require.NotEqual(t, delivery.PublishActionCreated, result.Action,
		"PublishActionCreated must NOT be assumed for an explicit skip; the no-reconstruction contract requires the actual action class")
}

// TestPublisher_Publish_P0_1_ConflictRename_ExistingMatch pins audit pin #6:
// when ConflictRename propagates through Publisher and the underlying
// uploader reports an existing match (PutActionRenamed), the resulting
// PublishResult must surface PublishActionRenamed. The audit pin
// "a new file is created under the new name" is enforced at the
// Uploader layer by Uploader.doPutFile's ConflictRename branch
// (Files.Create with renameWithTimestamp(original, UnixNano)); this
// publisher-layer pin proves the renamed-file outcome reaches the
// consumer.
func TestPublisher_Publish_P0_1_ConflictRename_ExistingMatch(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "video-folder-id"}
	files := &fakeFileUploader{
		existingFilenames: map[string]bool{"clip_abc.mp4": true},
	}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	result, err := pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      "/tmp/clip.mp4",
		Filename:       "clip_abc.mp4",
		Group:          "NBA News",
		Subject:        "video-xyz",
		ConflictPolicy: delivery.ConflictRename,
	})
	require.NoError(t, err)

	// (1) Policy was forwarded correctly.
	require.Equal(t, delivery.ConflictRename, files.uploadCalls[0].policy,
		"Publisher must forward ConflictRename to PutFileRequest verbatim")

	// (2) Audit pin #6: the underlying Uploader routed the existing-match
	// through ConflictRename → PutActionRenamed (a new file is created
	// under the new timestamped name), and Publisher translated that
	// to PublishActionRenamed at the delivery/drive boundary. The
	// "treat-as-sibling-row" branches downstream depend on this
	// exact Action class.
	require.Equal(t, PutActionRenamed, files.uploadCalls[0].routedAction,
		"behavioral fake must route to PutActionRenamed when filename exists + policy Rename — audit pin #6 closure")
	require.Equal(t, pub.actionFor(files.uploadCalls[0].routedAction), result.Action,
		"Publish must translate the routed PutAction to delivery.PublishAction at the boundary — no-reconstruction contract")
	require.NotEqual(t, delivery.PublishActionCreated, result.Action,
		"PublishActionCreated must NOT be assumed for a renamed conflict; the no-reconstruction contract requires the actual rename action class")
}

// ── PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE tests (July 2026) ──────────
//
// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE replaces the original log.Warn +
// silent-swallow in Publisher.resolveDestination Step 4 fall-through with a
// typed sentinel that callers can errors.Is. The sentinel is the canonical
// diagnostic for "PathBuilder failed + the caller supplied RootFolderOverride,
// so we fell back to direct-to-root upload" — useful for ops dashboards,
// smoke alerts, and aggressive-mode callers that want to fail-closed at the
// fallback (forward-pointer PR-VO-AGGREGATE-SUBPATH-CASCADE).
//
// godlike/07 typed-error contract: the sentinel is a top-level
// `var X = errors.New(...)` declared in errors.go for clean errors.Is probes.
// The wrap in resolveDestination uses dual-%w fmt.Errorf (Go 1.20+) to
// preserve BOTH the typed sentinel (errors.Is) AND the underlying
// PathBuilder cause (errors.As) — unlike the pre-PR fmt.Errorf "%%v+%%w"
// which stringified the cause and lost typed recovery. errors.Join is
// equally valid for typed-chain preservation but introduces newline-
// separated stderr noise that breaks single-line log aggregators; the
// dual-%w fmt.Errorf idiom is the canonical wrap for this surface.

// TestErrPathBuilderIncompleteForOverride_Sentinel pins the typed error
// declaration + dual-%w fmt.Errorf wrap contract.
//
// godlike/07 typed-error contract verification: rather than using
// errors.As(err, &recoveredCause) with a `*error` target (rejected by
// `go vet` because the second argument must be a concrete type, not
// the bare error interface), we verify typed-recovery via errors.Is
// against the SAME underlying-cause pointer-identity — errors.Is walks
// via Unwrap() and matches by == or Is(), achieving the same chain-
// preservation guarantee as errors.As without go vet flakiness. This
// also avoids walk-order dependency on the fmt.Errorf wrapErrors slice
// order (the first-match of `*error` was previously implementation-
// detail-dependent).
func TestErrPathBuilderIncompleteForOverride_Sentinel(t *testing.T) {
	var _ error = ErrPathBuilderIncompleteForOverride // compile-time pin

	// (a) Bare sentinel: errors.Is matches the error itself.
	require.ErrorIs(t, ErrPathBuilderIncompleteForOverride, ErrPathBuilderIncompleteForOverride,
		"bare sentinel MUST errors.Is match itself")

	// (b1) Production dual-%w fmt.Errorf wrap (canonical in resolveDestination).
	//      underlyingCause is held for the errors.Is identity probe at (c).
	underlyingCause := fmt.Errorf("group is required")
	wrapped := fmt.Errorf("delivery: PathBuilder failed under RootFolderOverride (cause: %w): %w",
		underlyingCause, ErrPathBuilderIncompleteForOverride)

	// (b2) errors.Is recovers the sentinel via wrap-chain walk.
	require.ErrorIs(t, wrapped, ErrPathBuilderIncompleteForOverride,
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
	require.Contains(t, wrapped.Error(), "RootFolderOverride",
		"dual-%w fmt.Errorf must preserve the sentinel's discriminator phrase (RootFolderOverride) for grep-able diagnostic surface")

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
	joined := errors.Join(joinedCause, ErrPathBuilderIncompleteForOverride)
	require.ErrorIs(t, joined, ErrPathBuilderIncompleteForOverride,
		"errors.Join downstream-compat: sentinel preserved for errors.Is")
	require.ErrorIs(t, joined, joinedCause,
		"errors.Join downstream-compat: underlying cause preserved for errors.Is (typed-recovery alias)")

	// (g) Negative control: unrelated sentinel does NOT match.
	otherSentinel := errors.New("drive: unrelated sentinel")
	require.NotErrorIs(t, wrapped, otherSentinel,
		"dual-%w wrapped sentinel MUST NOT match unrelated sentinels (errors.Is isolation)")
}

// TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel
// pins the canonical dual-return shape contract: when PathBuilder fails AND
// the caller supplied RootFolderOverride, resolveDestination returns BOTH a
// non-nil ResolvedDriveDestination struct (with direct-to-root fallback) AND
// the typed sentinel wrapped error. The dual-return is the load-bearing
// invariant that enables errors.Is probes at call sites without losing
// the resolved.FolderID needed for the upload. White-box test in same package.
func TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
		ConflictPolicy:     delivery.ConflictOverwrite,
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
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForOverride,
		"the returned error MUST wrap ErrPathBuilderIncompleteForOverride (errors.Is gateway for call-site decision)")

	// (3) Typed-chain preservation + grep-able diagnostic surface.
	//     The dual-%w fmt.Errorf wrap is verified by:
	//       (3a) err.Error() contains "group is required" (the underlying
	//            cause's message survives the wrap via the %w chain)
	//       (3b) errors.Is(err, ErrPathBuilderIncompleteForOverride)
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
	require.ErrorIs(t, err, ErrPathBuilderIncompleteForOverride,
		"dual-%w fmt.Errorf must preserve ErrPathBuilderIncompleteForOverride for errors.Is at the resolveDestination call site (call-site decision gateway)")
}

// TestResolveDestination_SuccessPath_ReturnsNilErr pins the success-path
// contract for resolveDestination: when PathBuilder succeeds + override set
// + segments non-empty, the function returns (resolved, nil). This is the
// PAIR to TestResolveDestination_PathBuilderFailOverride_ReturnsBothStructAndSentinel
// (which pins the fallback-path err=non-nil contract). Together the two
// tests lock the dual-return shape's err variable across both branches.
//
// godlike/07 typed-error regression-prevention: pre-PR-VO-ERR-PATHBUILDER-
// INCOMPLETE-OVERRIDE the finalize `return ..., nil` was silently zeroing
// out err even when the wrap was set. The bug was latent because the
// pre-PR log.Warn + err=nil discipline coincidentally matched. The
// fallback-path test surfaced the bug because it asserts err != nil;
// this success-path test prevents future regressions where someone might
// re-introduce `return ..., nil` (under the false assumption that the
// explicit nil is 'safer').
func TestResolveDestination_SuccessPath_ReturnsNilErr(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{result: "leaf-folder-id"}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	resolved, err := pub.resolveDestination(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		Group:              "test",
		Subject:            "abc",
		RootFolderOverride: "explicit-override-folder-id",
		ConflictPolicy:     delivery.ConflictOverwrite,
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
	require.Equal(t, []string{"abc"}, resolved.PathSegments,
		"success path: PathSegments must collapse to a single leaf under RootFolderOverride")
	require.Equal(t, "explicit-override-folder-id", resolved.RootFolderID,
		"success path: RootFolderID must be the explicit override (RootFolderOverride precedence)")
}

// TestResolveDestination_PathBuilderFailOverride_UsesOverrideRoot pins the
// end-to-end Publish swallow behavior: when resolveDestination returns typed
// sentinel + resolved struct, Publish errors.Is + log.Debug + uses override root.
func TestResolveDestination_PathBuilderFailOverride_UsesOverrideRoot(t *testing.T) {
	reg := testRegistry()
	folders := &fakeFolderManager{}
	files := &fakeFileUploader{}
	pub, err := NewPublisher(reg, folders, files, zap.NewNop())
	require.NoError(t, err)

	override := "explicit-fallback-folder-id"
	_, err = pub.Publish(context.Background(), delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
		ConflictPolicy:     delivery.ConflictOverwrite,
	})
	require.NoError(t, err,
		"Publish MUST swallow ErrPathBuilderIncompleteForOverride at the call-site (backward-compat per godlike/07 minimum-blast-radius)")

	// Upload landed in the override root via the resolved struct (proves dual-return shape contract).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Publish MUST use the override root from the resolved struct (dual-return shape contract)")
}

// ── PR-VO-SUBFOLDER tests (July 2026, commit c96eb1e0) ───────────────────
//
// PR-VO-SUBFOLDER fixes the invariant that callers with an explicit
// RootFolderOverride still benefit from the canonical PathBuilder
// structure (e.g. voiceover: voiceovers/{project}/{language}). The
// three tests below pin the three PathBuilder branches of resolveDestination:
//
//   1. PathBuilder succeeds + override set → subpath built under override.
//   2. PathBuilder fails + override set    → direct-to-root fallback (warn).
//   3. PathBuilder fails + no override     → error propagates to caller.
//
// They lock the contract that future refactors of PathBuilder or the
// registry-driven Resolve cannot silently break the PR-VO-SUBFOLDER
// contract — any drift surfaces here first.

// TestResolveDestination_VoiceoverWithRootFolderOverride_BuildsSubpath
// pins the canonical voiceover subpath structure under an explicit
// RootFolderOverride. Pre-PR-VO-SUBFOLDER the PathBuilder was
// short-circuited away when RootFolderOverride was non-empty, so the
// canonical voiceovers/{project}/{language} subtree was being
// SKIPPED and the MP3 landed directly in the override root. After
// the fix, PathBuilder runs first, segments under the override, and
// EnsureFolder is called with the canonical 2-segment structure.
//
// References:
//   - Fix: internal/infrastructure/drive/publisher.go::resolveDestination
//     (PR-VO-SUBFOLDER, Steps 4-6).
//   - VoiceoverPath: internal/application/assets/delivery/registry.go
//     (segments = [project, language] when both non-empty).
//   - SafeFolderName: pkg/pathutil/pathutil.go (preserves alphanum +
//     hyphen verbatim, so "storia-boxe-it" / "it-IT" pass through).
func TestResolveDestination_VoiceoverWithRootFolderOverride_BuildsSubpath(t *testing.T) {
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
		Destination:        delivery.DestinationVoiceover,
		LocalPath:          "/tmp/storia-boxe-it.mp3",
		Filename:           "storia-boxe-it.mp3",
		ProjectID:          "storia-boxe-it",
		Language:           "it-IT",
		RootFolderOverride: override,
		// ConflictPolicy omitted → registry-driven default applies
		// (Voiceover is ConflictSkip per P1.1 mapping).
	})
	require.NoError(t, err)

	// (1) EnsureFolder MUST be called with the EXPLICIT override as the
	//     parent (NOT the registry vo-root "vo-root") — the PR-VO-SUBFOLDER
	//     invariant: the override wins for the parent, the PathBuilder
	//     still owns the canonical subpath segments.
	require.Len(t, folders.ensureCalls, 1,
		"voiceover with RootFolderOverride MUST trigger exactly one EnsureFolder call (PR-VO-SUBFOLDER invariant)")
	require.Equal(t, override, folders.ensureCalls[0].parent,
		"EnsureFolder MUST be called with the explicit RootFolderOverride as parent — NOT the registry vo-root")
	require.Equal(t, []string{"storia-boxe-it", "it-IT"}, folders.ensureCalls[0].segments,
		"EnsureFolder MUST be called with the canonical voiceover subpath [{project},{language}] — SafeFolderName preserves alphanum + hyphen")

	// (2) Upload landed in the sub-folder returned by EnsureFolder (NOT
	//     the override root directly).
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, "voiceover-sub-folder-id", files.uploadCalls[0].folderID,
		"Upload MUST land in the canonical sub-folder returned by EnsureFolder")
}

// TestResolveDestination_PathBuilderFailsWithOverride_FallsBack pins
// the backward-compat fallback path: PathBuilder fails (missing
// metadata) AND the caller supplied RootFolderOverride. The fix
// logs a Warn and UPLOADS DIRECTLY into the override root (no
// EnsureFolder call). This is the production-PR-VO-SUBFOLDER contract
// — voiceover handlers that hit the legacy Group="" path still
// work without surfacing a typed error.
//
// Pre-PR-VO-SUBFOLDER this branch surface did not exist at all:
// PathBuilder was unconditionally skipped when override was set, so
// the SAME failure mode (no Group, no Subject, with override) would
// upload into the override root WITHOUT the warn signal, masking
// upstream metadata failures. The warn + typed continue-path is the
// load-bearing visibility surface.
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
		Destination:        delivery.DestinationYouTubeClip,
		LocalPath:          "/tmp/clip.mp4",
		Filename:           "clip.mp4",
		RootFolderOverride: override,
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
		"EnsureFolder MUST NOT be called when PathBuilder fails + RootFolderOverride is set (direct-to-root fallback)")

	// (2) Upload landed directly in the override root.
	require.Len(t, files.uploadCalls, 1)
	require.Equal(t, override, files.uploadCalls[0].folderID,
		"Upload MUST land in the explicit override root (direct-to-root fallback, no subfolder created)")

	// (3) Debug-level explicit-ack diagnostic surfaced the fallback so
	//     the operator sees the metadata gap in logs (NOT silent). The
	//     message text is the canonical 'incomplete subpath tolerated'
	//     ack that the call-site uses after errors.Is'ing
	//     ErrPathBuilderIncompleteForOverride.
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

// TestResolveDestination_PathBuilderFailsNoOverride_ReturnsError
// pins the AUTHORITATIVE error path: PathBuilder fails AND the caller
// did NOT supply RootFolderOverride. In this branch the PathBuilder
// failure is the canonical signal — the publisher MUST propagate it
// so the caller can fix the metadata (Group, Subject, ProjectID,
// Language, etc.). Silently degrading here would let typos in the
// metadata inputs slip through to a phantom upload into the registry's
// root folder — which is exactly the failure mode the fix's
// RequireSubpath gating is meant to prevent.
//
// Contrast with TestResolveDestination_PathBuilderFailsWithOverride_FallsBack:
// the override-accompanied failure is a legacy escape hatch;
// the no-override failure is the API-contract surface.
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
		// RootFolderOverride omitted (zero value).
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	require.Error(t, err,
		"PathBuilder failure with NO override MUST propagate to caller — the registry default would otherwise hide a metadata gap")
	require.Contains(t, err.Error(), "subject",
		"Error must surface PathBuilder's underlying 'subject (video ID) is required' — wrapping preserved so callers can errors.Is/As on it")
	require.Contains(t, err.Error(), "build path",
		"Error must include the publisher's 'delivery: build path for %q: %w' prefix so the canonical seam is grep-able in logs")
}

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
