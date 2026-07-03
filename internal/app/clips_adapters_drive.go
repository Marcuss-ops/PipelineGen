package app

import (
	"context"
	"fmt"
	"io"

	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// clipsDriveAdapter wraps drive.Admin + drive.Reader to satisfy
// clips.ClipDriveUploaderPort. FASE 9 Step 4 (June 2026): migrated
// from *drive.Uploader to Pattern 0 ports. GetFileMeta now uses
// Reader.GetFileMeta (returns *FileMeta with MimeType). ListFiles
// now uses Reader.SearchFiles (query-based listing). Both eliminate
// raw *gdrive.Service SDK access.
type clipsDriveAdapter struct {
	admin  drive.Admin
	reader drive.Reader
}

// Compile-time assertion: clipsDriveAdapter satisfies clips.ClipDriveUploaderPort.
var _ clips.ClipDriveUploaderPort = (*clipsDriveAdapter)(nil)

func newClipsDriveAdapter(admin drive.Admin, reader drive.Reader) clips.ClipDriveUploaderPort {
	if admin == nil {
		return nil
	}
	return &clipsDriveAdapter{admin: admin, reader: reader}
}

func (a *clipsDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	if a.admin == nil {
		return "", fmt.Errorf("clipsDriveAdapter: drive not wired")
	}
	return a.admin.GetOrCreateFolder(ctx, name, parentFolderID)
}

func (a *clipsDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.admin == nil {
		return "", fmt.Errorf("clipsDriveAdapter: drive not wired")
	}
	return a.admin.GetFolderName(ctx, folderID)
}

func (a *clipsDriveAdapter) TrashFolder(ctx context.Context, folderID string) error {
	if a.admin == nil {
		return fmt.Errorf("clipsDriveAdapter: drive not wired")
	}
	return a.admin.TrashFolder(ctx, folderID)
}

func (a *clipsDriveAdapter) DeleteFolder(ctx context.Context, folderID string) error {
	if a.admin == nil {
		return fmt.Errorf("clipsDriveAdapter: drive not wired")
	}
	return a.admin.DeleteFolder(ctx, folderID)
}

// UploadFile is the legacy drive upload seam. DRIVE-008 (July 2026):
// retired to fail-closed — converted to a typed-error stub that
// returns drive.ErrLegacySurfaceRetired on every invocation. The
// canonical upload path is delivery.Publisher.Publish (used by the
// bulk_upload_worker + UploadService after FASE 3). Production
// callers that still reach this seam receive a loud typed error at
// runtime per godlike/07 §"No fake availability" — no silent
// fallback, no faux-200 from a cancelled path.
//
// Compile-time assertion `var _ clips.ClipDriveUploaderPort =
// (*clipsDriveAdapter)(nil)` remains valid: the interface method
// signature is unchanged; the body is now a fail-closed shim that
// propagates the canonical sentinel via %w so callers can detect
// via errors.Is(err, drive.ErrLegacySurfaceRetired).
func (a *clipsDriveAdapter) UploadFile(ctx context.Context, localPath, folderID, filename string) (*clips.ClipUploadResultDTO, error) {
	return nil, fmt.Errorf("clipsDriveAdapter.UploadFile(localPath=%q folderID=%q filename=%q) retired by DRIVE-008: %w", localPath, folderID, filename, drive.ErrLegacySurfaceRetired)
}

// UploadFileWithDescription is the legacy drive upload seam with
// description metadata. DRIVE-008 (July 2026): retired to
// fail-closed — same shape as UploadFile above. Canonical path:
// delivery.Publisher.Publish(ctx, PublishRequest{Destination: ...,
// Description: description, ...}).
func (a *clipsDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*clips.ClipUploadResultDTO, error) {
	return nil, fmt.Errorf("clipsDriveAdapter.UploadFileWithDescription(localPath=%q folderID=%q filename=%q) retired by DRIVE-008: %w", localPath, folderID, filename, drive.ErrLegacySurfaceRetired)
}

func (a *clipsDriveAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.reader == nil {
		return nil, "", fmt.Errorf("clipsDriveAdapter: reader not wired")
	}
	return a.reader.DownloadFile(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if a.reader == nil {
		return "", fmt.Errorf("clipsDriveAdapter: reader not wired")
	}
	return a.reader.GetFileMD5(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMeta(ctx context.Context, fileID string) (*clips.ClipDriveFileMetaDTO, error) {
	if a.reader == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: reader not wired")
	}
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return &clips.ClipDriveFileMetaDTO{}, nil
	}
	return &clips.ClipDriveFileMetaDTO{MimeType: meta.MimeType}, nil
}

func (a *clipsDriveAdapter) TrashFile(ctx context.Context, fileID string) error {
	if a.admin == nil {
		return fmt.Errorf("clipsDriveAdapter: drive not wired")
	}
	return a.admin.TrashFile(ctx, fileID)
}

// ListFiles uses Reader.SearchFiles (query-based listing) and projects
// the result into ClipDriveFileDTO (only ID + Name consumed by handler).
func (a *clipsDriveAdapter) ListFiles(ctx context.Context, query string) ([]clips.ClipDriveFileDTO, error) {
	if a.reader == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: reader not wired")
	}
	files, err := a.reader.SearchFiles(ctx, query)
	if err != nil {
		return nil, err
	}
	if files == nil {
		return []clips.ClipDriveFileDTO{}, nil
	}
	out := make([]clips.ClipDriveFileDTO, len(files))
	for i, f := range files {
		out[i] = clips.ClipDriveFileDTO{ID: f.ID, Name: f.Name}
	}
	return out, nil
}

// ── Meta writer adapter ──────────────────────────────────────────

// clipMetaWriterAdapter wraps *semantic.MetadataWriter to satisfy
// clips.ClipMetaWriterPort. GeneratePayload translates the narrowed
// ClipMetaWriteRequest → concrete semantic.WriteRequest at the
// adapter boundary, executes the call, and projects the result onto
// ClipMetaPayload so callers never import the SDK.
type clipMetaWriterAdapter struct {
	inner *semantic.MetadataWriter
}

// Compile-time assertion: clipMetaWriterAdapter satisfies clips.ClipMetaWriterPort.
var _ clips.ClipMetaWriterPort = (*clipMetaWriterAdapter)(nil)

func newClipMetaWriterAdapter(w *semantic.MetadataWriter) clips.ClipMetaWriterPort {
	if w == nil {
		return nil
	}
	return &clipMetaWriterAdapter{inner: w}
}

func (a *clipMetaWriterAdapter) GeneratePayload(ctx context.Context, req clips.ClipMetaWriteRequest) (*clips.ClipMetaPayload, string, error) {
	concreteReq := semantic.WriteRequest{
		AssetID:   req.AssetID,
		AssetType: req.AssetType,
		MediaType: req.MediaType,
		Source:    req.Source,
		Generator: req.Generator,
		Style:     req.Style,
		Prompt:    req.Prompt,
		LocalPath: req.LocalPath,
	}
	payload, status, err := a.inner.GeneratePayload(ctx, concreteReq)
	if err != nil {
		return nil, status, err
	}
	if payload == nil {
		return nil, status, nil
	}
	return &clips.ClipMetaPayload{
		SearchText:          payload.SearchText,
		Tags:                payload.Tags,
		SemanticDescription: payload.SemanticDescription,
		RetrievalScore:      payload.RetrievalScore,
	}, status, nil
}
