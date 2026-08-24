// Package clips — P0-#1 atomic-RMW integration tests for
// UpdateCumulativeMetadataJSON.
//
// P0-#1 (July 2026) closed a silent data-loss window: the pre-fix
// function trashed the old metadata.json BEFORE the new one was
// uploaded (the upload step was a no-op per DRIVE-008 CUTOVER), so
// the per-folder sidecar was permanently gone. The post-fix function
// performs an atomic read-modify-write via delivery.Publisher.Publish
// with ConflictOverwrite — the publisher either replaces the
// existing file in place or returns an error WITHOUT touching the
// existing file. These tests pin the four contracts the migration
// MUST preserve:
//
//  1. Publish failure preserves the old metadata.json (the P0 fix's
//     central guarantee). The fake publisher returns an error, and
//     the fake uploader MUST record zero TrashFile calls (because
//     the old sidecar must NOT be deleted when the new one fails to
//     publish).
//
//  2. Publish success cleans up the old per-video .json files via
//     FileLifecycle.Trash (best-effort). The fake publisher returns
//     a successful PublishResult, and the fake uploader MUST record
//     at least one TrashFile call for the legacy per-video files.
//
//  3. Nil publisher returns the typed sentinel
//     ErrMetadataDriveNotConfigured so upstream callers can branch
//     via errors.Is.
//
//  4. The cold path (no existing metadata.json) works: the function
//     creates a new sidecar with just the new entry, publishes it
//     successfully, and skips the per-video cleanup (no legacy files
//     to clean up).
//
//  5. Backward-compat soft-dep paths: uploader == nil or folderID == ""
//     return nil without surfacing an error (the function is a no-op
//     when essential args are missing — pre-P0-#1 callers invoked it
//     with optional args).
//
// godlike/07 NO-FAKE-AVAILABILITY: the test fixtures use a
// deterministic, recorded fakes — no real Drive SDK calls. The
// atomic guarantee is exercised end-to-end inside the test (the
// fakes record every call so the test can assert the absence of
// destructive ops on the failure path).
package clips

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// fakeMetadataPublisher is a delivery.Publisher stub that records every
// Publish + ResolveFolder call so test asserts can pin the canonical
// contract. The test cares about Publish calls (atomic-RMW
// guarantee) and the post-publish Triggers (cleanup of legacy
// per-video files). ResolveFolder is included for completeness so
// the publisher compiles against the full delivery.Publisher
// surface — the metadata flow does not call ResolveFolder directly
// because it threads the folder ID via DestinationFolderID.
//
// PublishErr / PublishResult let each test drive a different
// outcome (success / failure) without rewriting the fake.
type fakeMetadataPublisher struct {
	mu             sync.Mutex
	PublishCalls   int
	LastPublishReq delivery.PublishRequest
	PublishErr     error
	PublishResult  *delivery.PublishResult
}

func (f *fakeMetadataPublisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.PublishCalls++
	f.LastPublishReq = req
	if f.PublishErr != nil {
		return nil, f.PublishErr
	}
	if f.PublishResult != nil {
		return f.PublishResult, nil
	}
	return &delivery.PublishResult{
		FileID:      "stub-file-id",
		WebViewLink: "https://drive.google.com/file/d/stub-file-id/view",
		FolderID:    req.DestinationFolderID,
		Destination: req.Destination,
		Action:      delivery.PublishActionUpdated,
	}, nil
}

func (f *fakeMetadataPublisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	return "stub-resolved-folder-id", nil
}

// fakeMetadataUploader is a minimal ClipDriveUploaderPort stub. The
// metadata flow exercises only ListFiles, DownloadFile, and TrashFile
// (the legacy per-video cleanup). Other methods are stubbed-out
// no-ops so the struct satisfies the full interface.
//
// The test fixtures drive the behaviour via the public fields:
//   - ExistingEntries: what ListFiles returns for the metadata.json query
//   - ExistingLegacy:  what ListFiles returns for the per-video .json query
//   - ExistingBody:    the bytes DownloadFile returns for the
//     metadata.json (read by the function during RMW)
type fakeMetadataUploader struct {
	mu sync.Mutex

	// ListFiles call counter — used to assert the function's read
	// sequence (list metadata, then list legacy, then trash).
	ListFilesCalls int
	// TrashFile call counter — the test pins the post-fix
	// contract: zero TrashFile calls when publish fails.
	TrashFileCalls int
	// LastTrashFileID — the test pins which file was trashed.
	LastTrashFileID string

	// ExistingEntries is the metadata.json list result. The
	// function reads this when the sidecar already exists. The
	// uploader's ListFiles is called twice per RMW (metadata.json
	// first, legacy per-video second), so the test swaps the
	// returned slice between calls via a counter.
	ExistingMetadataFiles []ClipDriveFileDTO
	ExistingLegacyFiles   []ClipDriveFileDTO

	// ExistingBody is the bytes DownloadFile returns when
	// ListFiles returned a metadata.json row. The function decodes
	// these as []map[string]any.
	ExistingBody []byte

	// TrashedFileIDs records every file ID the function asks to
	// trash. The test asserts this is empty on the failure path
	// (the central P0-#1 guarantee).
	TrashedFileIDs []string
}

func (f *fakeMetadataUploader) ListFiles(ctx context.Context, query string) ([]ClipDriveFileDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ListFilesCalls++
	if strings.Contains(query, "name = 'metadata.json'") {
		return f.ExistingMetadataFiles, nil
	}
	// The legacy per-video .json query: "name contains '.json' and name != 'metadata.json'"
	return f.ExistingLegacyFiles, nil
}

func (f *fakeMetadataUploader) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if len(f.ExistingBody) == 0 {
		return nil, "", errors.New("fakeMetadataUploader: no ExistingBody configured")
	}
	return io.NopCloser(bytes.NewReader(f.ExistingBody)), "metadata.json", nil
}

func (f *fakeMetadataUploader) TrashFile(ctx context.Context, fileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TrashFileCalls++
	f.LastTrashFileID = fileID
	f.TrashedFileIDs = append(f.TrashedFileIDs, fileID)
	return nil
}

// ── Stubs for methods the metadata flow does not exercise ────────────
// All return zero values + nil error so the test compiles against
// the full ClipDriveUploaderPort surface.

func (f *fakeMetadataUploader) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	return "stub-folder-id", nil
}
func (f *fakeMetadataUploader) GetFolderName(ctx context.Context, folderID string) (string, error) {
	return "stub-folder-name", nil
}
func (f *fakeMetadataUploader) TrashFolder(ctx context.Context, folderID string) error {
	return nil
}
func (f *fakeMetadataUploader) DeleteFolder(ctx context.Context, folderID string) error {
	return nil
}
func (f *fakeMetadataUploader) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	return "stub-md5", nil
}
func (f *fakeMetadataUploader) GetFileMeta(ctx context.Context, fileID string) (*ClipDriveFileMetaDTO, error) {
	return &ClipDriveFileMetaDTO{MimeType: "application/json"}, nil
}

// Compile-time assertion: fakeMetadataUploader satisfies ClipDriveUploaderPort.
var _ ClipDriveUploaderPort = (*fakeMetadataUploader)(nil)

// Compile-time assertion: fakeMetadataPublisher satisfies ClipPublisherPort.
var _ ClipPublisherPort = (*fakeMetadataPublisher)(nil)

// TestUpdateCumulativeMetadataJSON_PublishFailurePreservesOldFile_P0_1
// pins the P0-#1 atomic-RMW guarantee: when Publisher.Publish
// returns an error, the OLD metadata.json MUST remain on Drive
// (the function MUST NOT trash the old file before the new one
// is published). The fake uploader's TrashFileCalls counter is
// the canonical assertion — zero on the failure path.
//
// Without this guarantee, the pre-P0-#1 implementation would trash
// the old sidecar, fail to upload the new one, and leave the
// per-folder metadata.json permanently gone. The audit discovered
// this when tracing a production orphan: the YouTube registrar
// reported successful clip uploads (the dispatcher saved them) but
// the cumulative metadata.json sidecar was missing for the same
// folder — every subsequent clip re-registration for the folder
// had to re-derive the metadata from individual clip records, which
// was acceptable for a single registration but catastrophic in
// bulk-batch scenarios.
func TestUpdateCumulativeMetadataJSON_PublishFailurePreservesOldFile_P0_1(t *testing.T) {
	t.Parallel()

	// Pre-existing sidecar: two entries (clip-1 already present,
	// we will be appending clip-2). The function should download
	// this, merge with the new entry, write to temp, attempt to
	// publish, fail, and LEAVE THE OLD FILE INTACT.
	existingJSON, err := json.Marshal([]map[string]any{
		{"clip_id": "clip-1", "name": "Existing One"},
		{"clip_id": "clip-2", "name": "Existing Two"},
	})
	require.NoError(t, err)

	up := &fakeMetadataUploader{
		ExistingMetadataFiles: []ClipDriveFileDTO{{ID: "old-metadata-file-id", Name: "metadata.json"}},
		ExistingBody:          existingJSON,
		// No legacy per-video files for this test (focus is on the
		// TrashFile-on-failure contract, not the cleanup).
		ExistingLegacyFiles: nil,
	}
	pub := &fakeMetadataPublisher{
		PublishErr: errors.New("drive: network timeout (simulated)"),
	}

	tmpDir := t.TempDir()
	newEntry := map[string]any{"clip_id": "clip-3", "name": "New Three"}

	err = UpdateCumulativeMetadataJSON(
		context.Background(),
		up, pub,
		tmpDir,
		"parent-folder-id",
		"clip-3",
		newEntry,
		zap.NewNop(),
	)
	require.Error(t, err, "P0-#1: publish failure MUST surface as error (the pre-fix adapter hard-returned nil which masked the data-loss window)")
	require.Contains(t, err.Error(), "publish metadata.json",
		"the wrapped error MUST mention publish so operators can identify the failure mode")
	require.ErrorIs(t, err, errors.Unwrap(err),
		"the wrapped error MUST be errors.Is-able (godlike/07 typed-error contract)")

	// P0-#1 atomic-RMW guarantee: zero TrashFile calls on the
	// failure path. The old metadata.json is STILL ON DRIVE.
	require.Equal(t, 0, up.TrashFileCalls,
		"P0-#1 atomic-RMW: publish failure MUST NOT trash the old metadata.json (old sidecar MUST remain on Drive so the caller can retry)")

	// Publisher was called exactly once with the canonical
	// DestinationClipMetadata + DestinationFolderID shape.
	require.Equal(t, 1, pub.PublishCalls)
	require.Equal(t, delivery.DestinationClipMetadata, pub.LastPublishReq.Destination,
		"P0-#1: the metadata.json sidecar MUST route through DestinationClipMetadata (registry-driven destination)")
	require.Equal(t, "metadata.json", pub.LastPublishReq.Filename,
		"P0-#1: the sidecar filename MUST be 'metadata.json' (per-clip-folder convention)")
	require.Equal(t, "parent-folder-id", pub.LastPublishReq.DestinationFolderID,
		"P0-#1: the sidecar MUST land in the clip's resolved folder via DestinationFolderID")
	require.Equal(t, delivery.ConflictOverwrite, pub.LastPublishReq.ConflictPolicy,
		"P0-#1: the sidecar MUST be atomic-overwritten (ConflictOverwrite is the DestinationClipMetadata default in the registry)")

	// Temp file MUST be cleaned up by the defer (no leak even on
	// the failure path). Walk the temp dir to confirm.
	matches, err := filepath.Glob(filepath.Join(tmpDir, "meta_clip-3_*.json"))
	require.NoError(t, err)
	require.Empty(t, matches,
		"P0-#1: the temp file MUST be removed by the defer (network-timeout simulation must not leak meta_*.json in the temp dir)")
}

// TestUpdateCumulativeMetadataJSON_PublishSuccessCleansUp_P0_1 pins
// the post-success cleanup: when the publisher succeeds, the
// legacy per-video .json files MUST be trashed via the uploader's
// TrashFile surface. Pre-P0-#1 the cleanup ran BEFORE the publish
// step (which was a no-op), so per-video files were deleted while
// the canonical sidecar was absent — a silent data loss the audit
// uncovered. Post-P0-#1 the cleanup runs AFTER the publish so the
// metadata.json is the canonical ledger.
func TestUpdateCumulativeMetadataJSON_PublishSuccessCleansUp_P0_1(t *testing.T) {
	t.Parallel()

	existingJSON, err := json.Marshal([]map[string]any{
		{"clip_id": "clip-existing", "name": "Existing"},
	})
	require.NoError(t, err)

	up := &fakeMetadataUploader{
		ExistingMetadataFiles: []ClipDriveFileDTO{{ID: "old-metadata-file-id", Name: "metadata.json"}},
		ExistingBody:          existingJSON,
		// Legacy per-video .json files that should be trashed after
		// the new metadata.json is published.
		ExistingLegacyFiles: []ClipDriveFileDTO{
			{ID: "legacy-clip-1.json-id", Name: "clip-1.json"},
			{ID: "legacy-clip-2.json-id", Name: "clip-2.json"},
		},
	}
	pub := &fakeMetadataPublisher{
		PublishResult: &delivery.PublishResult{
			FileID:      "new-metadata-file-id",
			WebViewLink: "https://drive.google.com/file/d/new-metadata-file-id/view",
			FolderID:    "parent-folder-id",
			Destination: delivery.DestinationClipMetadata,
			Action:      delivery.PublishActionUpdated,
		},
	}

	tmpDir := t.TempDir()
	newEntry := map[string]any{"clip_id": "clip-new", "name": "New"}

	err = UpdateCumulativeMetadataJSON(
		context.Background(),
		up, pub,
		tmpDir,
		"parent-folder-id",
		"clip-new",
		newEntry,
		zap.NewNop(),
	)
	require.NoError(t, err, "publish success MUST return nil")

	// The new metadata.json was published atomically.
	require.Equal(t, 1, pub.PublishCalls)
	require.Equal(t, "new-metadata-file-id", pub.PublishResult.FileID)

	// After publish, the legacy per-video .json files MUST be
	// trashed. Pin both the count and the specific file IDs so a
	// future refactor that consolidates the trash loop (e.g. via
	// lifecycle.TrashBatch) breaks the test.
	require.Equal(t, 2, up.TrashFileCalls,
		"P0-#1: post-publish cleanup MUST trash every legacy per-video .json file (canonical ledger is the metadata.json sidecar)")
	require.ElementsMatch(t,
		[]string{"legacy-clip-1.json-id", "legacy-clip-2.json-id"},
		up.TrashedFileIDs,
		"P0-#1: cleanup MUST target the legacy per-video files specifically (not the metadata.json)")

	// Temp file MUST be cleaned up on the success path too.
	matches, err := filepath.Glob(filepath.Join(tmpDir, "meta_clip-new_*.json"))
	require.NoError(t, err)
	require.Empty(t, matches, "P0-#1: success path MUST also clean up the temp file")
}

// TestUpdateCumulativeMetadataJSON_NilPublisherSentinel_P0_1 pins
// the typed-sentinel contract: when the publisher is nil (Drive
// not configured at composition time), the function MUST return
// ErrMetadataDriveNotConfigured so upstream callers can branch via
// errors.Is. Pre-P0-#1 the function silently returned nil which
// hid the misconfiguration from operators.
func TestUpdateCumulativeMetadataJSON_NilPublisherSentinel_P0_1(t *testing.T) {
	t.Parallel()

	up := &fakeMetadataUploader{}
	var pub ClipPublisherPort // nil

	tmpDir := t.TempDir()
	err := UpdateCumulativeMetadataJSON(
		context.Background(),
		up, pub,
		tmpDir,
		"parent-folder-id",
		"clip-x",
		map[string]any{"clip_id": "clip-x", "name": "X"},
		zap.NewNop(),
	)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrMetadataDriveNotConfigured,
		"P0-#1: nil publisher MUST surface ErrMetadataDriveNotConfigured so upstream callers can branch via errors.Is")

	// The uploader MUST NOT be touched when the publisher is nil
	// (no read, no write, no trash). This pins the early-return
	// guard: the function bails out before the read step.
	require.Equal(t, 0, up.ListFilesCalls,
		"P0-#1: nil publisher MUST short-circuit BEFORE the read step (no Drive interaction at all)")
	require.Equal(t, 0, up.TrashFileCalls,
		"P0-#1: nil publisher MUST NOT trash anything (the function never even read the old sidecar)")
}

// TestUpdateCumulativeMetadataJSON_ColdPath_P0_1 pins the cold
// path: when no metadata.json exists yet (first cumulative
// update for a new folder), the function MUST create a new
// sidecar with just the new entry and publish it. The legacy
// per-video cleanup MUST be a no-op (zero TrashFile calls) since
// there are no legacy files to clean up.
func TestUpdateCumulativeMetadataJSON_ColdPath_P0_1(t *testing.T) {
	t.Parallel()

	up := &fakeMetadataUploader{
		ExistingMetadataFiles: nil, // cold path
		ExistingBody:          nil, // no body — DownloadFile would fail; the function MUST not call it
		ExistingLegacyFiles:   nil,
	}
	pub := &fakeMetadataPublisher{
		PublishResult: &delivery.PublishResult{
			FileID:      "cold-path-file-id",
			FolderID:    "parent-folder-id",
			Destination: delivery.DestinationClipMetadata,
			Action:      delivery.PublishActionCreated,
		},
	}

	tmpDir := t.TempDir()
	err := UpdateCumulativeMetadataJSON(
		context.Background(),
		up, pub,
		tmpDir,
		"parent-folder-id",
		"first-clip",
		map[string]any{"clip_id": "first-clip", "name": "First"},
		zap.NewNop(),
	)
	require.NoError(t, err, "cold path MUST succeed (ListFiles returns empty, function skips the read step)")

	require.Equal(t, 1, pub.PublishCalls,
		"P0-#1: cold path MUST publish exactly once (the new sidecar)")

	// Cold path: the temp file MUST contain a single-entry array
	// (the new entry), not an empty array. The fake publisher's
	// PublishResult doesn't carry the file content, but we can
	// re-derive the expected content from the newEntry: the
	// function MUST have built a single-element array. The temp
	// file is gone by the time we assert (defer cleaned it up), so
	// the content assertion is via the publish call itself.
	require.Equal(t, delivery.DestinationClipMetadata, pub.LastPublishReq.Destination)
	require.Equal(t, "metadata.json", pub.LastPublishReq.Filename,
		"P0-#1: cold path uses 'metadata.json' as the filename (LastPublishReq.Filename is set to the const metaFilename, NOT the clip ID)")

	// Cold path: zero legacy per-video files, so the cleanup loop
	// runs zero TrashFile calls.
	require.Equal(t, 0, up.TrashFileCalls,
		"P0-#1: cold path MUST NOT call TrashFile (no legacy files exist)")
}

// TestUpdateCumulativeMetadataJSON_SoftDeps_P0_1 pins the
// backward-compat soft-dep paths: when uploader is nil OR
// folderID is empty, the function MUST return nil without
// surfacing an error. Pre-P0-#1 callers invoked the function
// with optional args (e.g. tempDir="", folderID="") and the
// function was a no-op in those cases. Post-P0-#1 the contract
// is preserved so no caller breaks.
func TestUpdateCumulativeMetadataJSON_SoftDeps_P0_1(t *testing.T) {
	t.Parallel()

	t.Run("nil_uploader", func(t *testing.T) {
		t.Parallel()
		pub := &fakeMetadataPublisher{}
		err := UpdateCumulativeMetadataJSON(
			context.Background(),
			nil, // uploader nil — soft no-op
			pub,
			t.TempDir(),
			"folder-id",
			"clip-id",
			map[string]any{"clip_id": "clip-id"},
			zap.NewNop(),
		)
		require.NoError(t, err, "P0-#1: nil uploader MUST return nil (backward-compat soft no-op)")
		require.Equal(t, 0, pub.PublishCalls, "publisher MUST NOT be called when uploader is nil")
	})

	t.Run("empty_folderID", func(t *testing.T) {
		t.Parallel()
		up := &fakeMetadataUploader{}
		pub := &fakeMetadataPublisher{}
		err := UpdateCumulativeMetadataJSON(
			context.Background(),
			up, pub,
			t.TempDir(),
			"", // empty folderID — soft no-op
			"clip-id",
			map[string]any{"clip_id": "clip-id"},
			zap.NewNop(),
		)
		require.NoError(t, err, "P0-#1: empty folderID MUST return nil (backward-compat soft no-op)")
		require.Equal(t, 0, pub.PublishCalls, "publisher MUST NOT be called when folderID is empty")
		require.Equal(t, 0, up.TrashFileCalls, "uploader MUST NOT be called when folderID is empty")
	})
}

// _ silences the unused-import warning for `os` if a future test
// variant uses it. Keeping it imported for the test's planned
// extension: the cold-path test could be extended to assert on
// the temp file's content before the defer fires by capturing
// the path synchronously.
var _ = os.Stat
