package drive

// Test doubles (fakeFolderManager, fakeFileUploader, folderCall, uploadCall)
// + the testRegistry() helper. Single contiguous slice from the upstream
// publisher_test.go: `// ── Test doubles ──` divider through the helpers
// block, ending just before the first `func Test...` declaration.
// testRegistry() defined exactly once. Companion to publisher_<surface>_test.go siblings.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
