package app

import (
	"context"
	"fmt"
	"io"

	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// clipsDriveAdapter wraps *drive.Uploader to satisfy
// clips.ClipDriveUploaderPort. ListFiles propagates the raw Drive
// query string and projects the SDK File slice into the narrower
// ClipDriveFileDTO shape (only ID + Name consumed by the handler).
// GetFileMeta projects the SDK File into ClipDriveFileMetaDTO
// (only MimeType consumed).
type clipsDriveAdapter struct {
	up *drive.Uploader
}

// Compile-time assertion: clipsDriveAdapter satisfies clips.ClipDriveUploaderPort.
var _ clips.ClipDriveUploaderPort = (*clipsDriveAdapter)(nil)

func newClipsDriveAdapter(up *drive.Uploader) clips.ClipDriveUploaderPort {
	if up == nil {
		return nil
	}
	return &clipsDriveAdapter{up: up}
}

func (a *clipsDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetOrCreateFolder(ctx, name, parentFolderID)
}

func (a *clipsDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetFolderName(ctx, folderID)
}

func (a *clipsDriveAdapter) TrashFolder(ctx context.Context, folderID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.TrashFolder(ctx, folderID)
}

func (a *clipsDriveAdapter) DeleteFolder(ctx context.Context, folderID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.DeleteFolder(ctx, folderID)
}

func (a *clipsDriveAdapter) UploadFile(ctx context.Context, localPath, folderID, filename string) (*clips.ClipUploadResultDTO, error) {
	if a.up == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	res, err := a.up.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, err
	}
	return driveUploadToDTO(res), nil
}

func (a *clipsDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*clips.ClipUploadResultDTO, error) {
	if a.up == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	res, err := a.up.UploadFileWithDescription(ctx, localPath, folderID, filename, description)
	if err != nil {
		return nil, err
	}
	return driveUploadToDTO(res), nil
}

func (a *clipsDriveAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.up == nil {
		return nil, "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.DownloadFile(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetFileMD5(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMeta(ctx context.Context, fileID string) (*clips.ClipDriveFileMetaDTO, error) {
	if a.up == nil || a.up.Service == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	f, err := a.up.Service.Files.Get(fileID).Context(ctx).Fields("mimeType").Do()
	if err != nil {
		return nil, err
	}
	if f == nil {
		return &clips.ClipDriveFileMetaDTO{}, nil
	}
	return &clips.ClipDriveFileMetaDTO{MimeType: f.MimeType}, nil
}

func (a *clipsDriveAdapter) TrashFile(ctx context.Context, fileID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.TrashFile(ctx, fileID)
}

// ListFiles projects drive.Service.Files.List().Q(query)
// .Fields("files(id, name)").Context(ctx).Do() into the narrower
// ClipDriveFileDTO shape. The handler caller is responsible for
// including `trashed = false` in the query string.
func (a *clipsDriveAdapter) ListFiles(ctx context.Context, query string) ([]clips.ClipDriveFileDTO, error) {
	if a.up == nil || a.up.Service == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	list, err := a.up.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if list == nil {
		return []clips.ClipDriveFileDTO{}, nil
	}
	out := make([]clips.ClipDriveFileDTO, len(list.Files))
	for i, f := range list.Files {
		out[i] = clips.ClipDriveFileDTO{ID: f.Id, Name: f.Name}
	}
	return out, nil
}

// FileIsNotTrashed returns true when the Drive file is NOT in the
// user's trash. PR 5 (June 2026 — codex/clips-cleanup-job) added
// this for the assets.cleanup handler's classify-coherent
// verification. Maps to drive.Service.Files.Get(fileID).Fields("trashed").
func (a *clipsDriveAdapter) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if a.up == nil || a.up.Service == nil {
		return false, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	f, err := a.up.Service.Files.Get(fileID).Context(ctx).Fields("trashed").Do()
	if err != nil {
		return false, err
	}
	if f == nil {
		return false, nil
	}
	return !f.Trashed, nil
}

// driveUploadToDTO is the projection helper that maps drive.UploadResult
// onto the narrower clips.ClipUploadResultDTO. Drop any field the
// HTTP transport doesn't consume (Pattern 0 minimal projection).
func driveUploadToDTO(res *drive.UploadResult) *clips.ClipUploadResultDTO {
	if res == nil {
		return &clips.ClipUploadResultDTO{}
	}
	return &clips.ClipUploadResultDTO{
		FileID:       res.FileID,
		WebViewLink:  res.WebViewLink,
		DownloadLink: res.DownloadLink,
		MD5Checksum:  res.MD5Checksum,
	}
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
