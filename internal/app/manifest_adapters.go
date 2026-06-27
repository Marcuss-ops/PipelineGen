// Package app — manifest production adapter (PR 7 cutover).
//
// Wires internal/application/assets/manifest to the concrete
// *drive.Uploader.
//
// Upload-then-replace semantics: ReplaceManifest delegates to
// drive.Uploader.UploadFile, which internally runs a
// FindFileByName + atomic Files.Update-with-Media cycle (see
// internal/infrastructure/drive/uploader.go::doUploadFile).
// Files.Update is atomic on the Drive server, so observers never
// see a transient "empty folder" state between the old and new
// metadata.json writes — the canonical "upload-then-replace"
// spec is honored with no manual trash step.
//
// Per-folder serialization at the manifest.Service layer ensures
// concurrent writers to the SAME folder don't race on read-modify
// write; the SDK-side atomicity ensures readers of OTHER folders
// (or the same folder outside the lock window) never see the
// transient "file gone" state.
package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/manifest"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// driveAdapter implements manifest.DriveAdapter. Compiled with
// `var _ manifest.DriveAdapter = (*driveAdapter)(nil)` for the same
// drift-detection rationale as the clips adapters in
// internal/app/clips_adapters_drive.go.
type driveAdapter struct {
	up  *drive.Uploader
	log *zap.Logger
}

var _ manifest.DriveAdapter = (*driveAdapter)(nil)

// newManifestDriveAdapter wires the manifest adapter to *drive.Uploader.
// Nil-tolerant: returns nil when up == nil so the composition root can
// build a manifest.Service that ONLY supports UpsertLocal (Drive-less
// deployments / tests).
func newManifestDriveAdapter(up *drive.Uploader, log *zap.Logger) manifest.DriveAdapter {
	if up == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &driveAdapter{up: up, log: log}
}

// DownloadManifest: list → download → returns bytes (nil, nil) when
// no metadata.json exists.
func (a *driveAdapter) DownloadManifest(ctx context.Context, folderID, filename string) ([]byte, error) {
	if a.up == nil {
		return nil, fmt.Errorf("manifest driveAdapter: uploader not wired")
	}
	if folderID == "" || filename == "" {
		return nil, fmt.Errorf("manifest driveAdapter: empty folderID or filename")
	}
	if a.up.Service == nil {
		return nil, fmt.Errorf("manifest driveAdapter: uploader.service not wired")
	}
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, filename)
	list, err := a.up.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list metadata.json: %w", err)
	}
	if list == nil || len(list.Files) == 0 {
		return nil, nil
	}
	fileID := list.Files[0].Id
	body, _, err := a.up.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", fileID, err)
	}
	defer body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return buf.Bytes(), nil
}

// ReplaceManifest: upload-then-replace via drive.Uploader.UploadFile.
//
// The Uploader's internal flow (see
// internal/infrastructure/drive/uploader.go::doUploadFile) is:
//
//  1. FindFileByName(folderID, filename) — list for an existing
//     non-trashed file with the same name.
//  2. If found → Files.Update(existingID).Media(f) — atomic
//     server-side content replacement (same drive.FileId, new
//     bytes).
//  3. If not found → Files.Create(file).Media(f) — initial upload.
//
// Files.Update is atomic on Google's backend, so observers never
// see an "empty folder" state. There is intentionally NO explicit
// Trash step here: the previous Trash+Create pattern was a
// workaround for SDK-update risk, but the canonical Uploader path
// already does the right thing.
//
// Per-folder serialization is delegated to the manifest.Service
// caller (path-locked on `drive:<folderID>`); this adapter is
// single-shot and never observes a concurrent writer.
//
// On error the temporary local file is removed before returning
// so no orphan .tmp.json files accumulate.
func (a *driveAdapter) ReplaceManifest(ctx context.Context, folderID, filename string, content []byte) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("manifest driveAdapter: uploader not wired")
	}
	if folderID == "" || filename == "" {
		return "", fmt.Errorf("manifest driveAdapter: empty folderID or filename")
	}

	// Marshal to a temp local file so the canonical drive.Uploader
	// upload path can stream the content via .Media(f). Temp file
	// path is unique per call (UnixNano suffix) and is removed
	// before return.
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("mfst_%s_%d.json", filename, time.Now().UnixNano()))
	if err := os.WriteFile(tempPath, content, 0o644); err != nil {
		return "", fmt.Errorf("write temp: %w", err)
	}
	defer os.Remove(tempPath)

	// Delegate to UploadFile. Internally this does:
	//   FindFileByName → (Files.Update | Files.Create) → retry x3.
	// Inherits drive.Uploader's exponential-backoff retry policy on
	// transient errors (429, 503, timeout).
	res, err := a.up.UploadFile(ctx, tempPath, folderID, filename)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	if res == nil || res.FileID == "" {
		return "", fmt.Errorf("upload returned empty file id")
	}
	return res.FileID, nil
}
