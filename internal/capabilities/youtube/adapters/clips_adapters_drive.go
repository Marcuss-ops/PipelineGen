package adapters

import (
	"context"
	"fmt"
	"io"

	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// ClipsDriveAdapter wraps drive.Admin + drive.Reader to satisfy
// clips.ClipDriveUploaderPort. FASE 9 Step 4 (June 2026): migrated
// from *drive.Uploader to Pattern 0 ports. GetFileMeta now uses
// Reader.GetFileMeta (returns *FileMeta with MimeType). ListFiles
// now uses Reader.SearchFiles (query-based listing). Both eliminate
// raw *gdrive.Service SDK access.
type ClipsDriveAdapter struct {
	admin     drive.Admin
	reader    drive.Reader
	lifecycle drive.FileLifecycle
}

// Compile-time assertion: ClipsDriveAdapter satisfies clips.ClipDriveUploaderPort.
var _ clips.ClipDriveUploaderPort = (*ClipsDriveAdapter)(nil)

func NewClipsDriveAdapter(admin drive.Admin, reader drive.Reader, lifecycle drive.FileLifecycle) clips.ClipDriveUploaderPort {
	if admin == nil {
		return nil
	}
	return &ClipsDriveAdapter{admin: admin, reader: reader, lifecycle: lifecycle}
}

func (a *ClipsDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	if a.admin == nil {
		return "", fmt.Errorf("ClipsDriveAdapter: drive not wired")
	}
	return a.admin.GetOrCreateFolder(ctx, name, parentFolderID)
}

func (a *ClipsDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.admin == nil {
		return "", fmt.Errorf("ClipsDriveAdapter: drive not wired")
	}
	return a.admin.GetFolderName(ctx, folderID)
}

func (a *ClipsDriveAdapter) TrashFolder(ctx context.Context, folderID string) error {
	if a.admin == nil {
		return fmt.Errorf("ClipsDriveAdapter: drive not wired")
	}
	return a.admin.TrashFolder(ctx, folderID)
}

func (a *ClipsDriveAdapter) DeleteFolder(ctx context.Context, folderID string) error {
	if a.admin == nil {
		return fmt.Errorf("ClipsDriveAdapter: drive not wired")
	}
	return a.admin.DeleteFolder(ctx, folderID)
}

// UploadFile and UploadFileWithDescription removed in DRIVE-008 CUTOVER (July 2026).
// The legacy upload seams are retired; the canonical path is delivery.Publisher.Publish.
// The ClipDriveUploaderPort interface no longer carries these methods.

func (a *ClipsDriveAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.reader == nil {
		return nil, "", fmt.Errorf("ClipsDriveAdapter: reader not wired")
	}
	return a.reader.DownloadFile(ctx, fileID)
}

func (a *ClipsDriveAdapter) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if a.reader == nil {
		return "", fmt.Errorf("ClipsDriveAdapter: reader not wired")
	}
	return a.reader.GetFileMD5(ctx, fileID)
}

func (a *ClipsDriveAdapter) GetFileMeta(ctx context.Context, fileID string) (*clips.ClipDriveFileMetaDTO, error) {
	if a.reader == nil {
		return nil, fmt.Errorf("ClipsDriveAdapter: reader not wired")
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

func (a *ClipsDriveAdapter) TrashFile(ctx context.Context, fileID string) error {
	if a.lifecycle == nil {
		return fmt.Errorf("ClipsDriveAdapter: lifecycle not wired (P1-5 CUTOVER requires FileLifecycle)")
	}
	return a.lifecycle.Trash(ctx, fileID)
}

// ListFiles uses Reader.SearchFiles (query-based listing) and projects
// the result into ClipDriveFileDTO (only ID + Name consumed by handler).
func (a *ClipsDriveAdapter) ListFiles(ctx context.Context, query string) ([]clips.ClipDriveFileDTO, error) {
	if a.reader == nil {
		return nil, fmt.Errorf("ClipsDriveAdapter: reader not wired")
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

// ClipMetaWriterAdapter wraps semantic.MetadataWriterPort to satisfy
// clips.ClipMetaWriterPort. GeneratePayload translates the narrowed
// ClipMetaWriteRequest → concrete semantic.WriteRequest at the
// adapter boundary, executes the call, and projects the result onto
// ClipMetaPayload so callers never import the SDK.
type ClipMetaWriterAdapter struct {
	inner semantic.MetadataWriterPort
}

// Compile-time assertion: ClipMetaWriterAdapter satisfies clips.ClipMetaWriterPort.
var _ clips.ClipMetaWriterPort = (*ClipMetaWriterAdapter)(nil)

func (a *ClipMetaWriterAdapter) GeneratePayload(ctx context.Context, req clips.ClipMetaWriteRequest) (*clips.ClipMetaPayload, string, error) {
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
