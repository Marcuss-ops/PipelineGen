package drive

import (
	"context"
	"fmt"
	"strings"
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
	// probeCalls records each ProbeFolderAccess invocation. P1.3 (July 2026)
	// adds this tracking so the publisher tests can assert the validator
	// surface (kept lightweight — only the rootID matters, the SDK
	// response is irrelevant at this layer).
	probeCalls []string
	// probeErr, if set, is returned by ProbeFolderAccess. With
	// probeErrFn non-nil, probeErr is ignored and probeErrFn is
	// called per rootID (used for selective tests where some roots
	// fail and others pass).
	probeErr error
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
		// Group and Subject are empty → PathBuilder returns error
	})
	require.Error(t, err, "should reject when PathBuilder fails due to missing fields")
	require.Contains(t, err.Error(), "group")
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
//   YouTube / Artlist / Stock / Image / Voiceover / SoundEffect
//     → ConflictSkip (immutable / versioned assets)
//   Book / Script / Document
//     → ConflictOverwrite (regenerable, latest-version-wins outputs)
func TestRegistry_ConflictPolicyPerDestination_P1_1(t *testing.T) {
	reg := testRegistry()

	skipDestinations := []delivery.DestinationKey{
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationStock,
		delivery.DestinationImage,
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
		md5Checksum: "abc123def456",  // non-empty to observe propagation
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

