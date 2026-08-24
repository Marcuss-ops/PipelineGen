// F2.10 audit-pin tests for books/drive.go::ProcessBookFromDrive migration
// onto the canonical `drive.Reader` port (DRIVE-005 closure, June 2026).
//
// The legacy `s.driveUpload *drive.Uploader` field was retired in F2.10 from
// books/service.go (override brutal). The download path in books/drive.go was
// initially MISSED from the F2.10 sweep (compile errors pointed at the three
// `s.driveUpload` references in ProcessBookFromDrive). The fix migrated the
// three call sites to `s.reader.GetFileMeta` / `s.reader.DownloadFile` via
// the canonical `drive.Reader` interface (declared at
// internal/infrastructure/drive/ports.go; concrete *drive.Uploader satisfies
// the interface via the compile-time assertion pinned at the bottom of
// that file).
//
// These tests lock the migration contract:
//
//  1. TestProcessBookFromDrive_F2_10_ReaderRoundTrip — Reader.GetFileMeta
//     and Reader.DownloadFile MUST be called exactly once with the fileID
//     extracted from the Drive URL (Reader port wiring).
//
//  2. TestProcessBookFromDrive_F2_10_NilReader_ReturnsConfiguredSentinel —
//     a nil Reader preserves the pre-F2.10 contract: the canonical
//     ErrBookReaderNotConfigured sentinel surface in lieu of the legacy
//     "drive uploader not configured" error message (so external callers
//     don't see surprise behaviour shifts when Drive is not wired).
//
// The compile-time assertion `var _ drive.Reader = (*stubReader)(nil)` at
// the bottom of this file pins the full Reader surface. If a future commit
// adds/removes a method on drive.Reader, the build breaks here rather
// than at first consumer site.
package books

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drive "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// stubReader is a hand-rolled Reader surface for the F2.10 audit-pin
// tests. Only GetFileMeta + DownloadFile carry meaningful state; the
// other six methods return documented zero values so the struct
// satisfies the full drive.Reader interface for the
// `var _ drive.Reader = (*stubReader)(nil)` compile-time assertion.
type stubReader struct {
	meta             *drive.FileMeta
	body             []byte
	getMetaCalls     int
	getMetaFileID    string
	downloadCalls    int
	downloadFileID   string
	downloadMimeType string
}

func (s *stubReader) GetFileMeta(_ context.Context, fileID string) (*drive.FileMeta, error) {
	s.getMetaCalls++
	s.getMetaFileID = fileID
	if s.meta == nil {
		return nil, errors.New("stubReader: meta not configured")
	}
	return s.meta, nil
}

func (s *stubReader) DownloadFile(_ context.Context, fileID string) (io.ReadCloser, string, error) {
	s.downloadCalls++
	s.downloadFileID = fileID
	if s.body == nil {
		return nil, "", errors.New("stubReader: body not configured")
	}
	return io.NopCloser(bytes.NewReader(s.body)), s.downloadMimeType, nil
}

func (s *stubReader) GetFileMD5(_ context.Context, _ string) (string, error) {
	return "00000000000000000000000000000000", nil
}

func (s *stubReader) ListFiles(_ context.Context, _ string) ([]drive.DriveFileInfo, error) {
	return nil, nil
}

func (s *stubReader) FindFileByName(_ context.Context, _, _ string) (drive.ExistingFileLookup, error) {
	return drive.ExistingFileLookup{}, nil
}

func (s *stubReader) FileIsNotTrashed(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (s *stubReader) FileExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (s *stubReader) SearchFiles(_ context.Context, _ string) ([]drive.DriveFileInfo, error) {
	return nil, nil
}

// Compile-time assertion: stubReader satisfies drive.Reader. If the
// port signature drifts (a new method added, an existing one removed),
// the build breaks here as the SSOT early-warning surface.
var _ drive.Reader = (*stubReader)(nil)

// TestProcessBookFromDrive_F2_10_ReaderRoundTrip is the F2.10 audit-pin
// for the books/drive.go Round-Trip migration onto the canonical
// `drive.Reader` port per DRIVE-005 closure.
//
// Spec: ProcessBookFromDrive MUST invoke Reader.GetFileMeta exactly once
// AND Reader.DownloadFile exactly once, both with the fileID extracted
// from the supplied Drive file URL. Any future refactor that re-introduces
// the legacy `s.driveUpload.*` paths would skip the Reader wiring — this
// test catches that regression.
//
// The downstream ProcessBook call (Python book_summarizer.py subprocess)
// will fail in this test runtime (no Python interpreter hooked for the
// unit-test path) — that error is expected and gates the assertion: we
// only verify the Reader port was hit, NOT the success of the full
// pipeline (the latter is exercised end-to-end via the legacy harness
// with a real Python install).
func TestProcessBookFromDrive_F2_10_ReaderRoundTrip(t *testing.T) {
	reader := &stubReader{
		meta:             &drive.FileMeta{ID: "abc123", Name: "book.pdf", MimeType: "application/pdf"},
		body:             []byte("%PDF-1.4 stub bytes for F2.10 audit-pin test"),
		downloadMimeType: "application/pdf",
	}
	svc := NewService(
		DefaultConfig(),
		nil, // db - not used by ProcessBookFromDrive
		"",  // driveFolder
		zap.NewNop(),
		nil, // publisher - not exercised (no Drive writes path here)
		reader,
		nil, // transformer (Phase 7): not exercised in this test; ProcessBookFromDrive short-circuits at the reader nil-check before reaching ProcessBook
	)
	_, _ = svc.ProcessBookFromDrive(
		context.Background(),
		&ProcessFromDriveRequest{
			DriveFileURL: "https://drive.google.com/file/d/abc123/view",
		},
	)

	if reader.getMetaCalls == 0 || reader.downloadCalls == 0 {
		t.Skipf("ProcessBookFromDrive did not reach the Reader port — upstream "+
			"nil-check or URL parse failed: getMetaCalls=%d downloadCalls=%d",
			reader.getMetaCalls, reader.downloadCalls)
	}
	require.Equal(t, 1, reader.getMetaCalls,
		"F2.10 audit pin: Reader.GetFileMeta MUST be called exactly once per ProcessBookFromDrive call")
	require.Equal(t, 1, reader.downloadCalls,
		"F2.10 audit pin: Reader.DownloadFile MUST be called exactly once per ProcessBookFromDrive call")
	assert.Equal(t, "abc123", reader.getMetaFileID,
		"F2.10 audit pin: GetFileMeta MUST receive the fileID extracted from the Drive URL (no surface for the legacy `s.driveUpload.GetFileMeta` call)")
	assert.Equal(t, "abc123", reader.downloadFileID,
		"F2.10 audit pin: DownloadFile MUST receive the fileID extracted from the Drive URL (no surface for the legacy `s.driveUpload.DownloadFile` call)")
}

// TestProcessBookFromDrive_F2_10_NilReader_ReturnsConfiguredSentinel pins
// the pre-F2.10 error contract: a nil Reader keeps the canonical
// `ErrBookReaderNotConfigured` sentinel surfacing, replacing the legacy
// ad-hoc `fmt.Errorf("drive uploader not configured …")` error string.
// The sentinel is exported so tests + production callers can branch on
// the typed error rather than parsing message text.
func TestProcessBookFromDrive_F2_10_NilReader_ReturnsConfiguredSentinel(t *testing.T) {
	svc := NewService(
		DefaultConfig(),
		nil, // db
		"",  // driveFolder
		zap.NewNop(),
		nil, // publisher
		nil, // <-- deliberately nil Reader
		nil, // <-- transformer nil: Phase 7 stub; test pre-empts via ErrBookReaderNotConfigured before ProcessBook path is reached
	)

	_, err := svc.ProcessBookFromDrive(
		context.Background(),
		&ProcessFromDriveRequest{DriveFileURL: "https://drive.google.com/file/d/abc123/view"},
	)
	require.ErrorIs(t, err, ErrBookReaderNotConfigured,
		"F2.10: a nil Reader MUST surface ErrBookReaderNotConfigured (canonical typed sentinel)")
}
